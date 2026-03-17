package util

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

func GenerateCodeVerifier() string {
	b := make([]byte, 64)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func GenerateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
