package swarm

import "math"

// SetHave records that this node holds Segment idx (cached + verified). Idempotent;
// bumps the have epoch only when the set actually changes.
func (s *Swarm) SetHave(idx int) {
	if s.have[idx] {
		return
	}
	s.have[idx] = true
	s.haveEpoch++
}

// Have reports whether this node holds Segment idx.
func (s *Swarm) Have(idx int) bool { return s.have[idx] }

// Retain drops local haves outside the inclusive window [min,max] and returns the
// evicted indices (for the byte cache to drop). Bumps the epoch if anything changed.
func (s *Swarm) Retain(min, max int) []int {
	var evicted []int
	for idx := range s.have {
		if idx < min || idx > max {
			evicted = append(evicted, idx)
		}
	}
	if len(evicted) > 0 {
		for _, idx := range evicted {
			delete(s.have, idx)
		}
		s.haveEpoch++
	}
	return evicted
}

// HaveMsg encodes the local have-set as (base_index, bitmap, epoch) for SwarmHave.
// base is the lowest held index; bit i of the bitmap corresponds to base+i.
func (s *Swarm) HaveMsg() (base uint32, bitmap []byte, epoch uint64) {
	if len(s.have) == 0 {
		return 0, nil, s.haveEpoch
	}
	min, max := math.MaxInt, 0
	for idx := range s.have {
		if idx < min {
			min = idx
		}
		if idx > max {
			max = idx
		}
	}
	bits := make([]byte, (max-min)/8+1)
	for idx := range s.have {
		off := idx - min
		bits[off/8] |= 1 << uint(off%8)
	}
	return uint32(min), bits, s.haveEpoch
}

// decodeBitmap is the inverse of the (base,bitmap) encoding (test + peer-merge use).
func decodeBitmap(base uint32, bitmap []byte) []int {
	var out []int
	for i := 0; i < len(bitmap)*8; i++ {
		if bitmap[i/8]&(1<<uint(i%8)) != 0 {
			out = append(out, int(base)+i)
		}
	}
	return out
}
