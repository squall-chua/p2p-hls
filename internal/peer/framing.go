package peer

import (
	"sync/atomic"

	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
	"google.golang.org/protobuf/proto"
)

// encodeEnvelope marshals one control-channel message.
func encodeEnvelope(env *peerv1.Envelope) ([]byte, error) {
	return proto.Marshal(env)
}

// decodeEnvelope parses one control-channel message.
func decodeEnvelope(raw []byte) (*peerv1.Envelope, error) {
	var env peerv1.Envelope
	if err := proto.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// requestIDs allocates monotonic, per-connection request IDs starting at 1.
type requestIDs struct{ n atomic.Uint64 }

func (r *requestIDs) next() uint64 { return r.n.Add(1) }
