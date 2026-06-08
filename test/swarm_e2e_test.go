package e2e_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/squall-chua/p2p-hls/internal/app"
	"github.com/squall-chua/p2p-hls/internal/bridge"
	"github.com/squall-chua/p2p-hls/internal/catalog"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/library"
	"github.com/squall-chua/p2p-hls/internal/media"
	"github.com/squall-chua/p2p-hls/internal/signalserver"
	"github.com/stretchr/testify/require"
)

// countingMedia wraps the real media.Service and tallies Segment serves per name,
// so the test can prove the Host did not re-serve segments a peer got from another
// peer over the swarm. It implements peer.MediaHandler by delegation.
type countingMedia struct {
	inner *media.Service
	mu    sync.Mutex
	count map[string]int
}

func newCountingMedia(inner *media.Service) *countingMedia {
	return &countingMedia{inner: inner, count: map[string]int{}}
}

func (c *countingMedia) Playlist(remote identity.NodeID, contentID, name string) ([]byte, string, bool, error) {
	return c.inner.Playlist(remote, contentID, name)
}

func (c *countingMedia) Segment(remote identity.NodeID, contentID, name string) ([]byte, error) {
	c.mu.Lock()
	c.count[name]++
	c.mu.Unlock()
	return c.inner.Segment(remote, contentID, name)
}

func (c *countingMedia) OpenFile(remote identity.NodeID, contentID string) (io.ReadCloser, int64, error) {
	return c.inner.OpenFile(remote, contentID)
}

func (c *countingMedia) snapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.count))
	for k, v := range c.count {
		out[k] = v
	}
	return out
}

func (c *countingMedia) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := 0
	for _, v := range c.count {
		t += v
	}
	return t
}

// fetchAll drives a bridge to fetch every .ts listed in index.m3u8 and returns
// name -> bytes. It first GETs playlist.m3u8 (triggers transcode), then polls
// index.m3u8 until it lists at least minSegs segments.
func fetchAll(t *testing.T, prefix string, minSegs int) map[string][]byte {
	t.Helper()

	resp, err := http.Get(prefix + "playlist.m3u8")
	require.NoError(t, err)
	resp.Body.Close()

	var segs []string
	require.Eventually(t, func() bool {
		r, e := http.Get(prefix + "index.m3u8")
		if e != nil {
			return false
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		var found []string
		for _, line := range strings.Split(string(b), "\n") {
			l := strings.TrimSpace(line)
			if strings.HasSuffix(l, ".ts") {
				found = append(found, l)
			}
		}
		if len(found) >= minSegs {
			segs = found
			return true
		}
		return false
	}, 40*time.Second, 500*time.Millisecond)

	out := map[string][]byte{}
	for _, seg := range segs {
		sr, err := http.Get(prefix + seg)
		require.NoError(t, err)
		sb, _ := io.ReadAll(sr.Body)
		sr.Body.Close()
		require.Equal(t, http.StatusOK, sr.StatusCode, "segment %s", seg)
		require.NotEmpty(t, sb, "segment %s", seg)
		out[seg] = sb
	}
	return out
}

// TestSwarmRelaysAndOffloadsHost proves the party swarm works over REAL WebRTC:
// two viewers join one Watch Party; viewer A pulls every segment from the Host,
// then viewer B pulls the same segments from viewer A (not the Host) and gets
// byte-identical, integrity-verified bytes.
func TestSwarmRelaysAndOffloadsHost(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	// A longer sample so the event playlist lists several segments (~3 at hls_time=4).
	// Force a keyframe every 4s: the Host copies H.264 (no re-encode), so segments
	// only cut on existing keyframes — without forced keyframes the whole clip would
	// be a single segment.
	root := t.TempDir()
	src := filepath.Join(root, "sample.mp4")
	require.NoError(t, exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=12:size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=12",
		"-c:v", "libx264", "-c:a", "aac",
		"-force_key_frames", "expr:gte(t,n_forced*4)",
		"-shortest", src).Run())

	srv := httptest.NewServer(http.HandlerFunc(signalserver.New().HandleWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Host library (real ffprobe) + media service, wrapped in a counting handler.
	store, err := library.OpenStore(filepath.Join(t.TempDir(), "h.db"))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, library.NewScanner(store, library.FFProbe{}, []string{root}).ScanOnce(ctx))
	all, _ := store.All()
	require.Len(t, all, 1)
	cid := all[0].ContentID

	policy := catalog.NewPolicy(catalog.VisibilityPublic)
	mediaSvc := media.NewService(media.NewEngine(store, media.ExecRunner{}, t.TempDir()), policy)
	counting := newCountingMedia(mediaSvc)

	idHost, _ := identity.Generate()
	idA, _ := identity.Generate()
	idB, _ := identity.Generate()
	cfg := app.Config{SignalingURL: wsURL}

	host, err := app.NewNode(ctx, idHost, "host", cfg)
	require.NoError(t, err)
	defer host.Close()
	host.SetMedia(counting)
	host.StartParty(cid)

	hostID := idHost.NodeID()

	// --- Viewer A: join the party and pull every segment from the Host. ---
	viewerA, err := app.NewNode(ctx, idA, "viewerA", cfg)
	require.NoError(t, err)
	defer viewerA.Close()
	require.Eventually(t, func() bool { return viewerA.Sees(hostID) }, 5*time.Second, 25*time.Millisecond)
	require.NoError(t, viewerA.JoinParty(ctx, string(hostID), cid))

	brA := bridge.New(viewerA, "tok")
	require.NoError(t, brA.Start("127.0.0.1:0"))
	defer brA.Close()
	prefixA := brA.BaseURL() + "/s/tok/" + string(hostID) + "/" + cid + "/"

	segsA := fetchAll(t, prefixA, 2)
	require.GreaterOrEqual(t, len(segsA), 2, "host should produce at least 2 segments")
	numSegments := len(segsA)

	// A's fetches should have hit the Host: each listed segment served at least once.
	hostAfterA := counting.snapshot()
	for name := range segsA {
		require.GreaterOrEqual(t, hostAfterA[name], 1, "Host should have served %s to A", name)
	}

	// --- Viewer B: join the party. ---
	viewerB, err := app.NewNode(ctx, idB, "viewerB", cfg)
	require.NoError(t, err)
	defer viewerB.Close()
	require.Eventually(t, func() bool {
		return viewerB.Sees(hostID) && viewerB.Sees(idA.NodeID())
	}, 5*time.Second, 25*time.Millisecond)
	require.NoError(t, viewerB.JoinParty(ctx, string(hostID), cid))

	// Let the swarm connect A<->B and propagate A's have-map to B. Gossip ticks at
	// 1s; give several rounds plus WebRTC setup time. This is the determinism knob:
	// B must learn A's haves BEFORE it fetches, so it pulls from A, not the Host.
	time.Sleep(8 * time.Second)

	// Snapshot Host serve counts, then drive B's fetches through B's own bridge.
	hostBeforeB := counting.snapshot()
	brB := bridge.New(viewerB, "tok")
	require.NoError(t, brB.Start("127.0.0.1:0"))
	defer brB.Close()
	prefixB := brB.BaseURL() + "/s/tok/" + string(hostID) + "/" + cid + "/"
	segsB := fetchAll(t, prefixB, numSegments)
	require.Equal(t, numSegments, len(segsB), "B should fetch all segments")

	hostAfterB := counting.snapshot()

	// --- INTEGRITY: B's bytes equal A's bytes for every segment. ---
	for name, aBytes := range segsA {
		bBytes, ok := segsB[name]
		require.True(t, ok, "B missing segment %s", name)
		require.Equal(t, aBytes, bBytes, "B's bytes for %s must match A's (swarm relayed verified bytes)", name)
	}

	// --- OFFLOAD: the Host did not re-serve B's segments. ---
	// Per-segment: for each segment B fetched, the Host's count must not have risen
	// during B's fetch window (B got it from A via the swarm).
	offloaded := 0
	for name := range segsB {
		if hostAfterB[name] <= hostBeforeB[name] {
			offloaded++
		}
	}

	totalServes := counting.total()
	t.Logf("numSegments=%d hostAfterA=%v hostBeforeB=%v hostAfterB=%v totalHostServes=%d offloadedSegments=%d/%d",
		numSegments, hostAfterA, hostBeforeB, hostAfterB, totalServes, offloaded, numSegments)

	// Headline assertion: the swarm relayed at least some segments viewer-to-viewer,
	// so total Host serves are strictly below the no-swarm baseline (each of 2
	// viewers pulling every segment from the Host = 2*numSegments).
	require.Less(t, totalServes, 2*numSegments,
		"swarm should offload the Host below the no-swarm baseline (2*numSegments=%d)", 2*numSegments)

	// Stronger per-segment offload: most/all of B's segments came from A, not the Host.
	require.Equal(t, numSegments, offloaded,
		"every segment B fetched should have come from A (Host count flat)")
}
