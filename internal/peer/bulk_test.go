package peer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBulkFrameRoundTrip(t *testing.T) {
	payload := []byte("segment-bytes")
	raw := encodeBulkFrame(7, 3, true, payload)
	require.LessOrEqual(t, len(raw), FrameSize)

	id, seq, last, got, ok := decodeBulkFrame(raw)
	require.True(t, ok)
	require.Equal(t, uint64(7), id)
	require.Equal(t, uint32(3), seq)
	require.True(t, last)
	require.Equal(t, payload, got)
}

func TestDecodeRejectsShortFrame(t *testing.T) {
	_, _, _, _, ok := decodeBulkFrame([]byte{1, 2, 3})
	require.False(t, ok)
}

func TestPayloadMaxFitsInFrame(t *testing.T) {
	require.Equal(t, FrameSize, payloadMax+bulkHeaderSize)
}
