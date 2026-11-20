package reporting

// redact.go — secret scrubbing for problem reports (#36).
//
// The bundle includes logs and goroutine dumps, which can accidentally carry
// credentials. Every free-text field of a Capture passes through Redact()
// before it is persisted, so even a locally stored pending bundle is safe to
// share. The exact GitHub token in use and the mihomo controller secret are
// additionally injected as exact-match needles by ConfigureSecrets.

import (
	"regexp"
	"strings"
	"sync"
)

// Static token shapes (GitHub PATs, bearer headers, basic-auth URLs and
// "secret=..." style assignments).
var (
	reFineGrainedPAT = regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`)
	reClassicPAT     = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`)
	reBearer         = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9\-._~+/=]{16,}`)
	reAuthQuery      = regexp.MustCompile(`(?i)([?&](?:token|access_?token|key|api_?key|auth|secret|sig|sign|password)[a-z0-9_]*=)[^&\s"']+`)
	reKeyValue       = regexp.MustCompile(`(?i)(secret|token|password|passwd|pwd|api_?key)\w*\s*[=:]\s*["']?[^&\s"',;}{]{6,}`)
	reURLCreds       = regexp.MustCompile(`(?i)(https?://)([^/\s:@"]{1,64}):([^@\s"/]{4,})@`)
)

const redacted = "[REDACTED]"

var (
	needlesMu sync.Mutex
	needles   []string // exact strings forced to redacted (the live token, controller secret)
)

// ConfigureSecrets registers exact strings that must never appear in a
// report (the current GitHub token, the mihomo controller secret). Called by
// the Container whenever settings change.
func ConfigureSecrets(extra ...string) {
	needlesMu.Lock()
	defer needlesMu.Unlock()
	needles = needles[:0]
	for _, s := range extra {
		if t := strings.TrimSpace(s); len(t) >= 8 {
			needles = append(needles, t)
		}
	}
}

// Redact scrubs secrets from one string. Never returns nil.
func Redact(s string) string {
	if s == "" {
		return s
	}
	needlesMu.Lock()
	for _, n := range needles {
		if strings.Contains(s, n) {
			s = strings.ReplaceAll(s, n, redacted)
		}
	}
	needlesMu.Unlock()

	s = reFineGrainedPAT.ReplaceAllString(s, redacted)
	s = reClassicPAT.ReplaceAllString(s, redacted)
	s = reBearer.ReplaceAllString(s, "Bearer "+redacted)
	s = reAuthQuery.ReplaceAllString(s, "${1}"+redacted)
	s = reURLCreds.ReplaceAllString(s, "${1}${2}:"+redacted+"@")
	s = reKeyValue.ReplaceAllString(s, "${1}="+redacted)
	return s
}
