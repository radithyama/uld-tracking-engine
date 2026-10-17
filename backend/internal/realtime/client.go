package realtime

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = pongWait * 9 / 10
	maxInboundSize = 4096
	sendBuffer     = 256
)

// Client is one browser connection. gorilla/websocket permits at most one
// concurrent reader and one concurrent writer per connection, so all reads
// happen in readPump and all writes in writePump.
type Client struct {
	hub     *Hub
	conn    *websocket.Conn
	send    chan *websocket.PreparedMessage
	remote  string
	release func() // frees the connection-limit slot
}

// SnapshotFunc builds the initial full-state message for a new client.
type SnapshotFunc func(ctx context.Context) ([]byte, error)

type Handler struct {
	Hub       *Hub
	Log       *slog.Logger
	Snapshot  SnapshotFunc
	Upgrader  websocket.Upgrader
	Admission *Admission // connection limits (mount Admission.Precheck in front); nil admits all
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	release := func() {}
	if h.Admission != nil {
		var ok bool
		if release, ok = h.Admission.Acquire(w, r); !ok {
			return
		}
	}
	conn, err := h.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		release()
		return // Upgrade has already written an HTTP error
	}
	c := &Client{
		hub:     h.Hub,
		conn:    conn,
		send:    make(chan *websocket.PreparedMessage, sendBuffer),
		remote:  r.RemoteAddr,
		release: release,
	}

	// Register BEFORE reading the snapshot: any update published after this
	// point is buffered in c.send, so nothing falls in the gap between the
	// snapshot and the live stream. Updates that are already reflected in the
	// snapshot may be delivered again; the client discards them by version.
	if !h.Hub.Register(c) {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"), time.Now().Add(writeWait))
		_ = conn.Close()
		release()
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	snapshot, err := h.Snapshot(ctx)
	cancel()
	if err != nil {
		h.Log.Error("snapshot failed", "err", err)
		h.Hub.Unregister(c)
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "snapshot unavailable"), time.Now().Add(writeWait))
		_ = conn.Close()
		release()
		return
	}

	h.Log.Debug("websocket connected", "remote", c.remote, "clients", h.Hub.ClientCount())
	go c.writePump(snapshot)
	go c.readPump()
}

// readPump discards inbound data (the protocol is server->client) but must
// run to process control frames (pong, close) and detect dead peers.
func (c *Client) readPump() {
	defer func() {
		c.hub.Unregister(c)
		_ = c.conn.Close()
		c.release()
	}()
	c.conn.SetReadLimit(maxInboundSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// writePump is the connection's only writer. It sends the snapshot first,
// then drains the hub-fed send channel and keeps the connection alive with
// pings. It exits when the hub closes c.send or a write fails.
func (c *Client) writePump(snapshot []byte) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close() // unblocks readPump, which unregisters us
	}()

	_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	if err := c.conn.WriteMessage(websocket.TextMessage, snapshot); err != nil {
		return
	}

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseGoingAway, ""))
				return
			}
			if err := c.conn.WritePreparedMessage(msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
