package app

import (
	"encoding/json"
	"sync"
)

// Event is a control-plane notification fanned out to SSE subscribers.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// subscription is one SSE client's delivery channel.
type subscription struct {
	C  chan Event
	ch chan Event // same channel, writable side
}

// hub is an in-process fan-out. Publishers never block on a slow subscriber:
// a full subscriber buffer drops the event for that subscriber only.
type hub struct {
	mu   sync.Mutex
	subs map[*subscription]struct{}
}

func newHub() *hub { return &hub{subs: map[*subscription]struct{}{}} }

func (h *hub) subscribe() *subscription {
	ch := make(chan Event, 32)
	s := &subscription{C: ch, ch: ch}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *hub) unsubscribe(s *subscription) {
	h.mu.Lock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		close(s.ch)
	}
	h.mu.Unlock()
}

func (h *hub) publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		select {
		case s.ch <- e:
		default: // slow subscriber: drop this event for them
		}
	}
}

// Subscribe adapts the hub to bridge.Subscriber: it returns a channel of
// JSON-encoded events and a cancel func.
func (h *hub) Subscribe() (<-chan string, func()) {
	sub := h.subscribe()
	out := make(chan string, cap(sub.C))
	done := make(chan struct{})
	go func() {
		defer close(out)
		for {
			select {
			case <-done:
				return
			case e, ok := <-sub.C:
				if !ok {
					return
				}
				b, err := json.Marshal(e)
				if err != nil {
					continue
				}
				select {
				case out <- string(b):
				case <-done:
					return
				}
			}
		}
	}()
	return out, func() { close(done); h.unsubscribe(sub) }
}
