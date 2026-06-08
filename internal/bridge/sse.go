package bridge

import (
	"fmt"
	"net/http"
)

// Subscriber yields a stream of already-encoded JSON event strings and a cancel func.
type Subscriber interface {
	Subscribe() (<-chan string, func())
}

// SetEvents installs the event source for GET /api/events.
func (b *Bridge) SetEvents(s Subscriber) {
	b.mu.Lock()
	b.events = s
	b.mu.Unlock()
}

// handleSSE streams hub events as Server-Sent Events. EventSource cannot set
// headers, so the token rides the ?token= query.
func (b *Bridge) handleSSE(w http.ResponseWriter, r *http.Request) {
	if !b.originOK(r) || r.URL.Query().Get("token") != b.token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	b.mu.Lock()
	src := b.events
	b.mu.Unlock()
	flusher, ok := w.(http.Flusher)
	if src == nil || !ok {
		http.Error(w, "unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, cancel := src.Subscribe()
	defer cancel()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}
