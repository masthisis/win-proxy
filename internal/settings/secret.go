package settings

import (
	"crypto/rand"
	"encoding/hex"
)

// randomSecret returns a 32-char hex token used to authenticate the local
// mihomo external-controller API.
func randomSecret() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand never fails on supported platforms; degrade loudly.
		return "neurorift-insecure-secret"
	}
	return hex.EncodeToString(b)
}
