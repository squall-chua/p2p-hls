package catalog_test

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

type fakeParty struct {
	live    bool
	viewers int
	cid     string
}

func (f fakeParty) LiveParty(contentID string) (bool, int) {
	if contentID == f.cid {
		return f.live, f.viewers
	}
	return false, 0
}

// newServiceWithTitle (in service_test.go) returns (*Service, *Policy, *Requests)
// and seeds one Title whose Content ID is "cid-1".
const e2eTitleCID = "cid-1"

func TestBrowseAnnotatesLiveParty(t *testing.T) {
	svc, policy, _, _ := newServiceWithTitle(t)
	policy.AddAllow(identity.NodeID("bob"))
	svc.SetPartyProvider(fakeParty{live: true, viewers: 3, cid: e2eTitleCID})

	titles, err := svc.Browse(identity.NodeID("bob"))
	require.NoError(t, err)
	require.Len(t, titles, 1)
	require.True(t, titles[0].GetPartyLive())
	require.Equal(t, int32(3), titles[0].GetPartyViewers())
}

func TestBrowseNoProviderLeavesPartyFalse(t *testing.T) {
	svc, policy, _, _ := newServiceWithTitle(t)
	policy.AddAllow(identity.NodeID("bob"))
	titles, err := svc.Browse(identity.NodeID("bob"))
	require.NoError(t, err)
	require.False(t, titles[0].GetPartyLive())
}
