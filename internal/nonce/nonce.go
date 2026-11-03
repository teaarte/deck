package nonce

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
)

// Hex is n random bytes in hex; crypto/rand does not fail on the systems deck runs on.
func Hex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Token is 128 random bits for an address or a cookie: base64 that a URL carries as it is.
func Token() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
