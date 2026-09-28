package nonce

import (
	"crypto/rand"
	"encoding/hex"
)

// Hex is n random bytes in hex; crypto/rand does not fail on the systems deck runs on.
func Hex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
