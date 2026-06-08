package bridge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/squall-chua/p2p-hls/internal/bridge"
	"github.com/squall-chua/p2p-hls/internal/peer"
)

// fakeControl implements bridge.Control for handler tests.
type fakeControl struct {
	self     bridge.SelfView
	presence []bridge.PeerView
	library  []bridge.TitleView
	catalog  []bridge.TitleView
	catErr   error
	pending  []string
	approved []string
	reqMsg   string
	started  string
	joined   [2]string
	left     bool
	ended    string
}

func (f *fakeControl) Self() bridge.SelfView                { return f.self }
func (f *fakeControl) Presence() []bridge.PeerView          { return f.presence }
func (f *fakeControl) Library() ([]bridge.TitleView, error) { return f.library, nil }
func (f *fakeControl) Catalog(_ context.Context, _ string) ([]bridge.TitleView, error) {
	return f.catalog, f.catErr
}
func (f *fakeControl) RequestAccess(_ context.Context, _, msg string) error {
	f.reqMsg = msg
	return nil
}
func (f *fakeControl) PendingRequests() []string    { return f.pending }
func (f *fakeControl) Approve(p string) error       { f.approved = append(f.approved, p); return nil }
func (f *fakeControl) StartParty(cid string) string { f.started = cid; return "pid:" + cid }
func (f *fakeControl) JoinParty(_ context.Context, host, cid string) error {
	f.joined = [2]string{host, cid}
	return nil
}
func (f *fakeControl) LeaveParty()                 { f.left = true }
func (f *fakeControl) EndParty(reason string)      { f.ended = reason }
func (f *fakeControl) Audience() []bridge.PeerView { return f.presence }

func newTestBridge(t *testing.T, c bridge.Control) (*bridge.Bridge, string) {
	t.Helper()
	b := bridge.New(fakeStreamer{}, "secret-token")
	b.SetControl(c)
	if err := b.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b, b.BaseURL()
}

// apiGET issues an authenticated GET against the control API.
func apiGET(t *testing.T, base, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestAPISelf(t *testing.T) {
	c := &fakeControl{self: bridge.SelfView{NodeID: "n1", DisplayName: "Alice"}}
	_, base := newTestBridge(t, c)

	resp := apiGET(t, base, "/api/self")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got bridge.SelfView
	json.NewDecoder(resp.Body).Decode(&got)
	if got.DisplayName != "Alice" {
		t.Fatalf("got %+v", got)
	}
}

func TestAPIPresenceLibraryRequests(t *testing.T) {
	c := &fakeControl{
		presence: []bridge.PeerView{{NodeID: "n2", DisplayName: "Bob", Online: true}},
		library:  []bridge.TitleView{{ContentID: "cid1", DisplayTitle: "Movie"}},
		pending:  []string{"n3"},
	}
	_, base := newTestBridge(t, c)

	var peers []bridge.PeerView
	resp := apiGET(t, base, "/api/presence")
	json.NewDecoder(resp.Body).Decode(&peers)
	if len(peers) != 1 || peers[0].DisplayName != "Bob" {
		t.Fatalf("presence %+v", peers)
	}

	var lib []bridge.TitleView
	json.NewDecoder(apiGET(t, base, "/api/library").Body).Decode(&lib)
	if len(lib) != 1 || lib[0].ContentID != "cid1" {
		t.Fatalf("library %+v", lib)
	}

	var reqs []string
	json.NewDecoder(apiGET(t, base, "/api/requests").Body).Decode(&reqs)
	if len(reqs) != 1 || reqs[0] != "n3" {
		t.Fatalf("requests %+v", reqs)
	}
}

func apiPOST(t *testing.T, base, path, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestAPICatalogDeniedIs403(t *testing.T) {
	c := &fakeControl{catErr: peer.ErrDenied}
	_, base := newTestBridge(t, c)
	resp := apiGET(t, base, "/api/peers/n9/catalog")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %d", resp.StatusCode)
	}
}

func TestAPIRequestAccessAndApprove(t *testing.T) {
	c := &fakeControl{}
	_, base := newTestBridge(t, c)

	if r := apiPOST(t, base, "/api/peers/n9/request-access", `{"message":"please"}`); r.StatusCode != http.StatusAccepted {
		t.Fatalf("request-access status %d", r.StatusCode)
	}
	if c.reqMsg != "please" {
		t.Fatalf("message not passed: %q", c.reqMsg)
	}
	if r := apiPOST(t, base, "/api/requests/n3/approve", ""); r.StatusCode != 200 {
		t.Fatalf("approve status %d", r.StatusCode)
	}
	if len(c.approved) != 1 || c.approved[0] != "n3" {
		t.Fatalf("approved %+v", c.approved)
	}
}

func TestAPIPartyEndpoints(t *testing.T) {
	c := &fakeControl{}
	_, base := newTestBridge(t, c)

	resp := apiPOST(t, base, "/api/party/start", `{"contentId":"cidX"}`)
	var sp struct {
		PartyID string `json:"partyId"`
	}
	json.NewDecoder(resp.Body).Decode(&sp)
	if sp.PartyID != "pid:cidX" || c.started != "cidX" {
		t.Fatalf("start %+v / %q", sp, c.started)
	}
	if r := apiPOST(t, base, "/api/party/join", `{"hostNodeId":"h1","contentId":"cidY"}`); r.StatusCode != 200 {
		t.Fatalf("join %d", r.StatusCode)
	}
	if c.joined != [2]string{"h1", "cidY"} {
		t.Fatalf("joined %+v", c.joined)
	}
	if r := apiPOST(t, base, "/api/party/leave", ""); r.StatusCode != 200 || !c.left {
		t.Fatalf("leave %d %v", r.StatusCode, c.left)
	}
	if r := apiPOST(t, base, "/api/party/end", ""); r.StatusCode != 200 || c.ended == "" {
		t.Fatalf("end %d %q", r.StatusCode, c.ended)
	}
}

func TestAPIAudience(t *testing.T) {
	c := &fakeControl{presence: []bridge.PeerView{{NodeID: "h", DisplayName: "Host"}}}
	_, base := newTestBridge(t, c)
	var got []bridge.PeerView
	json.NewDecoder(apiGET(t, base, "/api/party/audience").Body).Decode(&got)
	if len(got) != 1 {
		t.Fatalf("audience %+v", got)
	}
	if got[0].NodeID != "h" {
		t.Fatalf("wrong member %+v", got)
	}
}

func TestAPIRejectsMissingToken(t *testing.T) {
	_, base := newTestBridge(t, &fakeControl{})
	resp, _ := http.Get(base + "/api/self") // no Authorization header
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %d", resp.StatusCode)
	}
}
