package peer

import "encoding/binary"

const (
	// FrameSize is the max bulk-channel message size (browser-interop ceiling).
	FrameSize      = 16 * 1024
	bulkHeaderSize = 13 // 8 (requestId) + 4 (seq) + 1 (flags)
	payloadMax     = FrameSize - bulkHeaderSize
	flagLast       = 0x01
	bulkHighWater  = 1 << 20   // pause sending above 1 MiB buffered
	bulkLowWater   = 256 << 10 // resume below 256 KiB
)

func encodeBulkFrame(requestID uint64, seq uint32, last bool, payload []byte) []byte {
	frame := make([]byte, bulkHeaderSize+len(payload))
	binary.BigEndian.PutUint64(frame[0:8], requestID)
	binary.BigEndian.PutUint32(frame[8:12], seq)
	if last {
		frame[12] = flagLast
	}
	copy(frame[bulkHeaderSize:], payload)
	return frame
}

func decodeBulkFrame(raw []byte) (requestID uint64, seq uint32, last bool, payload []byte, ok bool) {
	if len(raw) < bulkHeaderSize {
		return 0, 0, false, nil, false
	}
	requestID = binary.BigEndian.Uint64(raw[0:8])
	seq = binary.BigEndian.Uint32(raw[8:12])
	last = raw[12]&flagLast != 0
	payload = raw[bulkHeaderSize:]
	return requestID, seq, last, payload, true
}
