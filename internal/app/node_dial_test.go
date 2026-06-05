package app

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestShouldDialIsLowerNodeID(t *testing.T) {
	require.True(t, shouldDial(identity.NodeID("aaa"), identity.NodeID("bbb")))  // self < peer
	require.False(t, shouldDial(identity.NodeID("ccc"), identity.NodeID("bbb"))) // self > peer
}

func TestNodeImplementsSwarmTransport(t *testing.T) {
	var _ swarmTransport = (*Node)(nil) // compile-time interface satisfaction
}
