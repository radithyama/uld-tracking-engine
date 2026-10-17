// Package httpx contains small HTTP helpers shared by the API services.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func WriteError(w http.ResponseWriter, status int, msg string, details any) {
	body := map[string]any{"error": msg}
	if details != nil {
		body["details"] = details
	}
	WriteJSON(w, status, body)
}

// Logger is a slog-based request logger.
func Logger(l *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)
			l.Debug("http",
				"method", r.Method, "path", r.URL.Path, "status", ww.Status(),
				"bytes", ww.BytesWritten(), "dur_ms", time.Since(start).Milliseconds(),
				"req_id", middleware.GetReqID(r.Context()))
		})
	}
}

// Serve runs srv until ctx is cancelled, then shuts down gracefully.
func Serve(ctx context.Context, l *slog.Logger, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		l.Info("http listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	l.Info("http shutting down")
	return srv.Shutdown(shutdownCtx)
}

// HandleHealthcheckCommand implements `<binary> healthcheck <url>`: it GETs
// the URL and exits 0 on HTTP 200, 1 otherwise. Used as the Docker
// HEALTHCHECK command, since the distroless runtime image has no curl/wget.
// It returns normally when the process was not invoked that way.
func HandleHealthcheckCommand() {
	if len(os.Args) < 3 || os.Args[1] != "healthcheck" {
		return
	}
	// Probes only this container's own loopback listener.
	u, err := url.Parse(os.Args[2])
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		os.Exit(2)
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(u.String()) // #nosec G704 -- loopback-only URL validated above
	if err != nil {
		os.Exit(1)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		os.Exit(1)
	}
	os.Exit(0)
}

// Recoverer converts handler panics into a 500 and a single structured JSON
// log line (chi's default recoverer prints a multi-line plain-text stack to
// stderr, which breaks line-oriented JSON log collection).
func Recoverer(l *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					l.Error("panic in http handler",
						"error.message", fmt.Sprint(rec),
						"error.stack_trace", string(debug.Stack()),
						"url.path", r.URL.Path,
						"http.request.id", middleware.GetReqID(r.Context()))
					WriteError(w, http.StatusInternalServerError, "internal error", nil)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
