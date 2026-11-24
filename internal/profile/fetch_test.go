package profile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestFetch_HeadersApplied (issue #75 custom-header half): extra headers are
// applied verbatim AFTER the defaults and win over User-Agent/Accept.
func TestFetch_HeadersApplied(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(200)
		_, _ = w.Write([]byte("proxies: []"))
	}))
	defer srv.Close()

	f := NewFetcher("", time.Second)
	f.UserAgent = "some-ua"
	f.Headers = map[string]string{
		"Authorization": "Bearer panel-token",
		"Cookie":        "session=abc",
		"User-Agent":    "explicit-ua",
		"Bad Key":       "dropped (invalid name)",
		"":              "dropped",
		"Empty":         "",
	}
	if _, err := f.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Get("Authorization") != "Bearer panel-token" {
		t.Errorf("Authorization header missing: %v", got)
	}
	if got.Get("Cookie") != "session=abc" {
		t.Errorf("Cookie header missing: %v", got)
	}
	if got.Get("User-Agent") != "explicit-ua" {
		t.Errorf("explicit UA must win: %q", got.Get("User-Agent"))
	}
	if len(got["Bad Key"]) != 0 {
		t.Errorf("invalid header name must be dropped")
	}
}

// TestSanitizeHeaders: empty rows dropped, caps enforced.
func TestSanitizeHeaders(t *testing.T) {
	if got := sanitizeHeaders(map[string]string{"A": "b", "": "x", "Empty": "", "  K  ": "  v  "}); len(got) != 2 {
		t.Errorf("sanitizeHeaders = %v, want 2 clean entries", got)
	}
	if got := sanitizeHeaders(nil); got != nil {
		t.Errorf("nil must stay nil, got %v", got)
	}
	big := map[string]string{}
	for i := 0; i < 40; i++ {
		big[string(rune('A'+i%26))+string(rune('a'+i%26))+string(rune('b'+i%26))] = "v"
	}
	if got := sanitizeHeaders(big); len(got) > 16 {
		t.Errorf("cap not enforced: %d entries", len(got))
	}
}
