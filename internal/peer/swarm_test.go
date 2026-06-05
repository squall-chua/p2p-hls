package peer

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/identity"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"github.com/stretchr/testify/require"
)

type fakeSwarm struct {
	gotHave *peerv1.SwarmHave
	segReq  *peerv1.GetSwarmSegment
}

func (f *fakeSwarm) OnSwarmHave(_ identity.NodeID, h *peerv1.SwarmHave) { f.gotHave = h }
func (f *fakeSwarm) SwarmSegment(_ identity.NodeID, r *peerv1.GetSwarmSegment) ([]byte, error) {
	f.segReq = r
	return []byte("SEG"), nil
}

func TestSetSwarmHandlerStored(t *testing.T) {
	s := &Session{}
	h := &fakeSwarm{}
	s.SetSwarmHandler(h)
	require.Equal(t, SwarmHandler(h), s.currentSwarmHandler())
}
