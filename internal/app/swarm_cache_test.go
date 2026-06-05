package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSegCachePutGetRetain(t *testing.T) {
	c := newSegCache()
	c.put(3, []byte("three"))
	c.put(10, []byte("ten"))
	got, ok := c.get(3)
	require.True(t, ok)
	require.Equal(t, []byte("three"), got)

	c.retain(5, 15) // drop indices outside [5,15]
	_, ok = c.get(3)
	require.False(t, ok)
	_, ok = c.get(10)
	require.True(t, ok)
}
