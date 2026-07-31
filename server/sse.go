package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// event is a single SSE message.
type event struct {
	Type string `json:"type"` // e.g. "task", "file", "qr", "status"
	Data any    `json:"data"`
}

// broker fans events out to any number of SSE subscribers. A nil broker is
// valid and drops all events (useful in tests).
type broker struct {
	mu   sync.Mutex
	subs map[chan event]struct{}
}

func newBroker() *broker {
	return &broker{subs: make(map[chan event]struct{})}
}

func (b *broker) publish(typ string, data any) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- event{Type: typ, Data: data}:
		default: // slow consumer; drop to avoid blocking publishers
		}
	}
}

func (b *broker) subscribe() chan event {
	ch := make(chan event, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *broker) unsubscribe(ch chan event) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
	close(ch)
}

// serveHTTP streams events from ch to the client as SSE until the request is
// cancelled. It first sends a comment so proxies/browsers flush headers.
func serveSSE(w http.ResponseWriter, r *http.Request, ch chan event) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx-style buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
			flusher.Flush()
		}
	}
}
