package peer

import (
	"errors"
	"fmt"

	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

// Sentinel RPC errors. Handlers return these; the wire layer maps them to/from
// Error.Status codes.
var (
	ErrDenied      = errors.New("access denied")
	ErrNotFound    = errors.New("not found")
	ErrUnavailable = errors.New("unavailable")
)

func statusOf(err error) peerv1.Error_Status {
	switch {
	case errors.Is(err, ErrDenied):
		return peerv1.Error_DENIED
	case errors.Is(err, ErrNotFound):
		return peerv1.Error_NOT_FOUND
	case errors.Is(err, ErrUnavailable):
		return peerv1.Error_UNAVAILABLE
	default:
		return peerv1.Error_INTERNAL
	}
}

func statusErr(e *peerv1.Error) error {
	base := fmt.Errorf("remote error: %s", e.GetDetail())
	switch e.GetStatus() {
	case peerv1.Error_DENIED:
		base = ErrDenied
	case peerv1.Error_NOT_FOUND:
		base = ErrNotFound
	case peerv1.Error_UNAVAILABLE:
		base = ErrUnavailable
	}
	if e.GetDetail() != "" {
		return fmt.Errorf("%w: %s", base, e.GetDetail())
	}
	return base
}

func errEnvelope(reqID uint64, err error) *peerv1.Envelope {
	return &peerv1.Envelope{
		RequestId: reqID,
		Body:      &peerv1.Envelope_Error{Error: &peerv1.Error{Status: statusOf(err), Detail: err.Error()}},
	}
}
