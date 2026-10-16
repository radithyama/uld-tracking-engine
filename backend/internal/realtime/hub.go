// Package realtime fans live updates out to WebSocket clients.
package realtime

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// Hub owns the set of connected clients. All mutation of that set happens on
// the Run goroutine, so no mutex is needed; other goroutines communicate with
// it exclusively through channels.
//
// Invariant: only the hub goroutine sends on or closes a client's send
// channel, which makes the close-after-send panic impossible.
type Hub struct {
	log        *slog.Logger
	register   chan *Client
	unregister chan *Client
	broadcast  chan *websocket.PreparedMessage
	done       chan struct{}
	clients    map[*Client]struct{}
	count      atomic.Int64
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		log:        log,
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan *websocket.PreparedMessage, 1024),
		done:       make(chan struct{}),
		clients:    make(map[*Client]struct{}),
	}
}

// Run is the hub's event loop. On ctx cancellation it closes every client's
// send channel, which makes their write pumps send a close frame and exit.
func (h *Hub) Run(ctx context.Context) {
	defer close(h.done)
	for {
		select {
		case <-ctx.Done():
			for c := range h.clients {
				h.drop(c)
			}
			return

		case c := <-h.register:
			h.clients[c] = struct{}{}
			h.count.Store(int64(len(h.clients)))

		case c := <-h.unregister:
			h.drop(c)

		case msg := <-h.broadcast:
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// Client can't keep up; disconnect it rather than let one
					// slow browser stall every other client. It will
					// reconnect and resync from a fresh snapshot.
					h.log.Warn("dropping slow websocket client", "remote", c.remote)
					h.drop(c)
				}
			}
		}
	}
}

func (h *Hub) drop(c *Client) {
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
		h.count.Store(int64(len(h.clients)))
	}
}

// Register adds a client; it returns false if the hub has shut down.
func (h *Hub) Register(c *Client) bool {
	select {
	case h.register <- c:
		return true
	case <-h.done:
		return false
	}
}

func (h *Hub) Unregister(c *Client) {
	select {
	case h.unregister <- c:
	case <-h.done:
	}
}

// Broadcast queues a payload for every client. The frame is encoded once via
// PreparedMessage and reused for all connections.
func (h *Hub) Broadcast(payload []byte) error {
	pm, err := websocket.NewPreparedMessage(websocket.TextMessage, payload)
	if err != nil {
		return err
	}
	select {
	case h.broadcast <- pm:
	case <-h.done:
	}
	return nil
}

func (h *Hub) ClientCount() int64 { return h.count.Load() }
