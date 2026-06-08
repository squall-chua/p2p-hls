package app

import (
	"testing"
	"time"
)

func TestHubFanOutToSubscribers(t *testing.T) {
	h := newHub()
	a := h.subscribe()
	b := h.subscribe()
	h.publish(Event{Type: "presence", Data: map[string]any{"n": 1}})

	for _, ch := range []<-chan Event{a.C, b.C} {
		select {
		case e := <-ch:
			if e.Type != "presence" {
				t.Fatalf("got %q", e.Type)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
}

func TestHubDropsSlowSubscriberWithoutBlocking(t *testing.T) {
	h := newHub()
	s := h.subscribe() // never drained; buffer is small
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			h.publish(Event{Type: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a slow subscriber")
	}
	h.unsubscribe(s)
}

func TestHubUnsubscribeStopsDelivery(t *testing.T) {
	h := newHub()
	s := h.subscribe()
	h.unsubscribe(s)
	h.publish(Event{Type: "x"})
	select {
	case _, ok := <-s.C:
		if ok {
			t.Fatal("received after unsubscribe")
		}
	case <-time.After(100 * time.Millisecond):
		// acceptable: closed channel or no delivery
	}
}
