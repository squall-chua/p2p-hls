package bridge_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/bridge"
)

func TestServesSPAWithInjectedToken(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	b.SetBootstrap("n1", "Alice")
	_ = b.Start("127.0.0.1:0")
	t.Cleanup(func() { b.Close() })

	resp, err := http.Get(b.BaseURL() + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, `window.__P2P__`) || !strings.Contains(s, "secret-token") || !strings.Contains(s, "Alice") {
		t.Fatalf("token not injected: %s", s)
	}
}

func TestIndexHTMLInjectsToken(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	b.SetBootstrap("n1", "Alice")
	_ = b.Start("127.0.0.1:0")
	t.Cleanup(func() { b.Close() })

	// Don't follow redirects: /index.html must serve the injected page directly
	// (200), behaving identically to "/" rather than 301-bouncing to "./".
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(b.BaseURL() + "/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/index.html status = %d, want 200 (injected, not a redirect)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, `window.__P2P__`) || !strings.Contains(s, "secret-token") {
		t.Fatalf("/index.html not injected: %s", s)
	}
}

func TestSPAFallbackServesIndexForUnknownRoute(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	b.SetBootstrap("n1", "Alice")
	_ = b.Start("127.0.0.1:0")
	t.Cleanup(func() { b.Close() })
	resp, _ := http.Get(b.BaseURL() + "/peer/n2") // client route, not a file
	if resp.StatusCode != 200 {
		t.Fatalf("fallback status %d", resp.StatusCode)
	}
}

func TestInjectBootstrapMarker(t *testing.T) {
	boot := `<script>window.__P2P__={"token":"t"}</script>`
	out := bridge.InjectBootstrapForTest(`<body><!--__P2P_BOOTSTRAP__--></body>`, boot)
	if !strings.Contains(out, boot) || strings.Contains(out, "__P2P_BOOTSTRAP__") {
		t.Fatalf("marker path: %s", out)
	}
}

func TestInjectBootstrapFallbackBeforeHead(t *testing.T) {
	boot := `<script>window.__P2P__={"token":"t"}</script>`
	// Nuxt SPA shell: no marker, but has </head>
	out := bridge.InjectBootstrapForTest(`<html><head><title>x</title></head><body><div id="__nuxt"></div></body></html>`, boot)
	if !strings.Contains(out, boot) {
		t.Fatalf("fallback did not inject: %s", out)
	}
	// must be injected BEFORE </head> (so it runs before the app mounts)
	if strings.Index(out, boot) > strings.Index(out, "</head>") {
		t.Fatalf("bootstrap not before </head>: %s", out)
	}
}

func TestInjectBootstrapNoHeadPrepends(t *testing.T) {
	boot := `<script>window.__P2P__={"token":"t"}</script>`
	out := bridge.InjectBootstrapForTest(`<div id="__nuxt"></div>`, boot)
	if !strings.HasPrefix(out, boot) {
		t.Fatalf("no-head path should prepend: %s", out)
	}
}

func TestBootstrapEscapesScriptBreakout(t *testing.T) {
	b := bridge.New(fakeStreamer{}, "secret-token")
	b.SetBootstrap("n1", "</script><script>alert(1)</script>")
	_ = b.Start("127.0.0.1:0")
	t.Cleanup(func() { b.Close() })
	resp, _ := http.Get(b.BaseURL() + "/")
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	// The injected name must be escaped and never break out of the bootstrap
	// <script>. json.Marshal HTML-escapes < and > to their unicode forms, so
	// the raw breakout payload must not appear literally while its escaped form
	// must. Both assertions hold regardless of how many real <script> tags the
	// bundle itself ships.
	if strings.Contains(s, "<script>alert(1)") {
		t.Fatalf("script breakout not escaped: %s", s)
	}
	if !strings.Contains(s, `\u003cscript\u003ealert(1)`) {
		t.Fatalf("expected escaped payload, got: %s", s)
	}
}
