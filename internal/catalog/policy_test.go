package catalog_test

import (
	"testing"

	"github.com/squall-chua/p2p-hls/internal/catalog"
	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestRestrictedPolicyAllowsOnlyAllowList(t *testing.T) {
	a, b := identity.NodeID("alice"), identity.NodeID("bob")
	p := catalog.NewPolicy(catalog.VisibilityRestricted)
	require.False(t, p.Allowed(a))
	p.AddAllow(a)
	require.True(t, p.Allowed(a))
	require.False(t, p.Allowed(b))
}

func TestPublicPolicyAllowsExceptBlocked(t *testing.T) {
	a := identity.NodeID("alice")
	p := catalog.NewPolicy(catalog.VisibilityPublic)
	require.True(t, p.Allowed(a))
	p.AddBlock(a)
	require.False(t, p.Allowed(a), "block overrides public")
}

func TestRequestsAddListApprove(t *testing.T) {
	r := catalog.NewRequests()
	n := identity.NodeID("bob")
	r.Add(n, "let me in")
	require.Equal(t, []identity.NodeID{n}, r.List())
	msg, ok := r.Take(n)
	require.True(t, ok)
	require.Equal(t, "let me in", msg)
	require.Empty(t, r.List())
}
