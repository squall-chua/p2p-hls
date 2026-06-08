package bridge_test

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/bridge"
)

// fakeEvents is an in-test Subscriber: Subscribe returns a channel the test pushes to.
type fakeEvents struct{ ch chan string }

func (f *fakeEvents) Subscribe() (<-chan string, func()) { return f.ch, func() {} }

func TestSSEStreamsEvents(t *testing.T) {
	fe := &fakeEvents{ch: make(chan string, 1)}
	b := bridge.New(fakeStreamer{}, "secret-token")
	b.SetEvents(fe)
	if err := b.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })

	req, _ := http.NewRequest(http.MethodGet, b.BaseURL()+"/api/events?token=secret-token", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type %q", ct)
	}

	fe.ch <- `{"type":"presence"}`
	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data:") {
				line <- sc.Text()
				return
			}
		}
	}()
	select {
	case got := <-line:
		if !strings.Contains(got, "presence") {
			t.Fatalf("data line %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no SSE data received")
	}
}

func TestSSERejectsBadToken(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	b.SetEvents(&fakeEvents{ch: make(chan string)})
	_ = b.Start("127.0.0.1:0")
	t.Cleanup(func() { b.Close() })
	resp, _ := http.Get(b.BaseURL() + "/api/events?token=wrong")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %d", resp.StatusCode)
	}
}
