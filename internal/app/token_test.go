package app

import "testing"

func TestNewTokenIsRandomHex(t *testing.T) {
	a, b := NewToken(), NewToken()
	if a == b || len(a) < 16 {
		t.Fatalf("weak token a=%q b=%q", a, b)
	}
}
