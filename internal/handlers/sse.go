package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"momopay/internal/realtime"
)

const heartbeatInterval = 25 * time.Second

// serveSSE streams events published on topic to the client until it
// disconnects. It mirrors PocketBase's own realtime endpoint: the server's
// global http.Server.WriteTimeout (5min) is disabled per-connection so a
// long-lived SSE stream isn't cut off, and a periodic comment keeps
// intermediate proxies from closing the idle connection.
func serveSSE(e *core.RequestEvent, hub *realtime.Hub, topic string) error {
	rc := http.NewResponseController(e.Response)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return e.InternalServerError("Failed to initialize SSE connection.", err)
	}

	e.Response.Header().Set("Content-Type", "text/event-stream")
	e.Response.Header().Set("Cache-Control", "no-store")
	e.Response.Header().Set("X-Accel-Buffering", "no")
	e.Response.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return err
	}

	ch, unsubscribe := hub.Subscribe(topic)
	defer unsubscribe()

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-e.Request.Context().Done():
			return nil
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			writeSSEEvent(e.Response, ev)
			rc.Flush()
		case <-heartbeat.C:
			fmt.Fprint(e.Response, ": ping\n\n")
			rc.Flush()
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, ev realtime.Event) {
	fmt.Fprintf(w, "event: %s\n", ev.Name)
	for _, line := range strings.Split(ev.Data, "\n") {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprint(w, "\n")
}
