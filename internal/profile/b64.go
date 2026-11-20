package profile

import (
	"encoding/base64"
	"strings"
)

// decodeB64 decodes standard, url-safe, padded and unpadded base64 strings.
// Subscription payloads in the wild use every combination, so the decoder
// tries each variant until one succeeds.
func decodeB64(s string) ([]byte, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	// Some servers pad with dots or spaces; strip them.
	s = strings.NewReplacer(".", "", " ", "", "\n", "", "\r", "").Replace(s)
	encodings := []*base64.Encoding{
		base64.RawStdEncoding,
		base64.StdEncoding,
		base64.RawURLEncoding,
		base64.URLEncoding,
	}
	for _, enc := range encodings {
		if b, err := enc.DecodeString(s); err == nil {
			return b, true
		}
	}
	// Last resort: fix padding length then retry std.
	s = strings.TrimRight(s, "=")
	if pad := len(s) % 4; pad != 0 {
		s += strings.Repeat("=", 4-pad)
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, true
	}
	return nil, false
}

// isProbablyBase64 reports whether s consists only of base64 characters and
// is long enough to plausibly be encoded payload.
func isProbablyBase64(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 24 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '+' || r == '/' || r == '-' || r == '_' || r == '=' || r == '.':
		default:
			return false
		}
	}
	return true
}
