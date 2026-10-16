package realtime

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newTestServer(t *testing.T) (*Hub, *httptest.Server, context.CancelFunc) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := NewHub(log)
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	srv := httptest.NewServer(&Handler{
		Hub: hub, Log: log,
		Snapshot: func(context.Context) ([]byte, error) { return []byte(`{"type":"snapshot"}`), nil },
	})
	t.Cleanup(func() { cancel(); srv.Close() })
	return hub, srv, cancel
}

func dial(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func waitClients(t *testing.T, hub *Hub, n int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() != n {
		if time.Now().After(deadline) {
			t.Fatalf("want %d clients, have %d", n, hub.ClientCount())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSnapshotThenBroadcastToAllClients(t *testing.T) {
	hub, srv, _ := newTestServer(t)

	const n = 20
	conns := make([]*websocket.Conn, n)
	for i := range conns {
		conns[i] = dial(t, srv)
		defer conns[i].Close()
	}
	waitClients(t, hub, n)

	// Broadcast from many goroutines at once to exercise the hub under -race.
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = hub.Broadcast([]byte(`{"type":"uld_update"}`)) }()
	}
	wg.Wait()

	for i, c := range conns {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, first, err := c.ReadMessage()
		if err != nil || !strings.Contains(string(first), "snapshot") {
			t.Fatalf("client %d: first message should be snapshot, got %q (%v)", i, first, err)
		}
		for j := range 10 {
			if _, msg, err := c.ReadMessage(); err != nil || !strings.Contains(string(msg), "uld_update") {
				t.Fatalf("client %d update %d: %q %v", i, j, msg, err)
			}
		}
	}
}

func TestClientDisconnectUnregisters(t *testing.T) {
	hub, srv, _ := newTestServer(t)
	c := dial(t, srv)
	waitClients(t, hub, 1)
	_ = c.Close()
	waitClients(t, hub, 0)
	// Broadcasting with no clients must not block or panic.
	_ = hub.Broadcast([]byte(`{}`))
}

func TestSlowClientIsDropped(t *testing.T) {
	hub, srv, _ := newTestServer(t)
	c := dial(t, srv) // never reads
	defer c.Close()
	waitClients(t, hub, 1)

	// Large frames fill the TCP buffers quickly; once the writer blocks the
	// send buffer fills and the hub must drop the client instead of stalling.
	big := []byte(`{"pad":"` + strings.Repeat("x", 64<<10) + `"}`)
	for range sendBuffer * 4 {
		_ = hub.Broadcast(big)
	}
	waitClients(t, hub, 0)
}

func TestShutdownClosesClients(t *testing.T) {
	hub, srv, cancel := newTestServer(t)
	c := dial(t, srv)
	defer c.Close()
	waitClients(t, hub, 1)
	cancel()

	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err := c.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseGoingAway) {
				t.Fatalf("expected going-away close, got %v", err)
			}
			return
		}
	}
}
