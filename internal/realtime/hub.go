// Package realtime is an in-process pub/sub hub used to push Server-Sent
// Events to browsers. It is intentionally single-instance: it holds no
// state beyond open Go channels, so it does not survive a restart and does
// not work across multiple server processes.
package realtime

import "sync"

// Event is one SSE message: an event name plus a pre-rendered HTML payload.
type Event struct {
	Name string
	Data string
}

// Hub fans out Events published on a topic to every subscriber of that topic.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan Event]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[chan Event]struct{})}
}

// Subscribe registers a new listener on topic and returns the channel to
// read events from plus an unsubscribe func that MUST be called (typically
// deferred) when the client disconnects.
func (h *Hub) Subscribe(topic string) (<-chan Event, func()) {
	ch := make(chan Event, 8)

	h.mu.Lock()
	if h.subs[topic] == nil {
		h.subs[topic] = make(map[chan Event]struct{})
	}
	h.subs[topic][ch] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		delete(h.subs[topic], ch)
		if len(h.subs[topic]) == 0 {
			delete(h.subs, topic)
		}
		h.mu.Unlock()
		close(ch)
	}

	return ch, unsubscribe
}

// Publish broadcasts an event to every current subscriber of topic.
// Slow/unresponsive subscribers are dropped for that message rather than
// blocking the publisher (the hook that triggered it).
func (h *Hub) Publish(topic string, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[topic] {
		select {
		case ch <- ev:
		default:
		}
	}
}
