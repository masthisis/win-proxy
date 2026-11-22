package pacserve

import (
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T) (*Server, int) {
	t.Helper()
	s := New(slog.New(slog.DiscardHandler))
	port, err := s.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop() })
	return s, port
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

func TestServeDefaultScript(t *testing.T) {
	s, port := testServer(t)
	s.SetContent([]byte(DefaultScript("127.0.0.1:7897")))

	resp, body := get(t, s.URL())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != ContentType {
		t.Fatalf("content-type = %q, want %q", ct, ContentType)
	}
	if !strings.Contains(body, "function FindProxyForURL") {
		t.Fatal("served script is missing FindProxyForURL")
	}
	if !strings.Contains(body, "PROXY 127.0.0.1:7897") {
		t.Fatalf("served script missing the PROXY line:\n%s", body)
	}
	if !strings.Contains(body, "172.16.0.0") {
		t.Fatal("default script must keep 172.16/12 private ranges DIRECT")
	}
	if port <= 0 {
		t.Fatalf("port = %d, want a real bound port", port)
	}
	if want := "http://127.0.0.1:" + strconv.Itoa(port) + Path; s.URL() != want {
		t.Fatalf("URL() = %q, want %q", s.URL(), want)
	}
}

func TestOtherPathsAndMethods(t *testing.T) {
	s, _ := testServer(t)
	s.SetContent([]byte("function FindProxyForURL(url, host) { return \"DIRECT\"; }"))

	client := &http.Client{Timeout: 3 * time.Second}
	resp, _ := get(t, "http://127.0.0.1:"+strconv.Itoa(s.Port())+"/other")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /other status = %d, want 404", resp.StatusCode)
	}
	hr, err := client.Head(s.URL())
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	hr.Body.Close()
	if hr.StatusCode != http.StatusOK {
		t.Fatalf("HEAD /pac status = %d, want 200", hr.StatusCode)
	}
	pr, err := client.Post(s.URL(), "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	pr.Body.Close()
	if pr.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /pac status = %d, want 405", pr.StatusCode)
	}
}

func TestHotReload(t *testing.T) {
	s, _ := testServer(t)
	s.SetContent([]byte("function FindProxyForURL(url, host) { return \"PROXY 1.2.3.4:1\"; }"))
	_, body := get(t, s.URL())
	if !strings.Contains(body, "1.2.3.4:1") {
		t.Fatalf("stale content served after SetContent:\n%s", body)
	}
	s.SetContent([]byte("function FindProxyForURL(url, host) { return \"PROXY 5.6.7.8:2\"; }"))
	_, body = get(t, s.URL())
	if !strings.Contains(body, "5.6.7.8:2") {
		t.Fatalf("hot reload did not take effect:\n%s", body)
	}
}

func TestStopIsIdempotentAndClearsState(t *testing.T) {
	s, port := testServer(t)
	if err := s.Stop(); err != nil {
		t.Fatalf("first stop: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	if s.Running() {
		t.Fatal("Running() = true after Stop")
	}
	if s.URL() != "" {
		t.Fatalf("URL() = %q after Stop, want empty", s.URL())
	}
	if s.Port() != 0 {
		t.Fatalf("Port() = %d after Stop, want 0", port)
	}
	// Restarting after a stop must work (mode switching toggles this).
	if _, err := s.Start(0); err != nil {
		t.Fatalf("restart after stop: %v", err)
	}
}

func TestStartTwiceFails(t *testing.T) {
	s, _ := testServer(t)
	if _, err := s.Start(0); err == nil {
		t.Fatal("second Start on a running server must fail")
	}
}

func TestDefaultScript(t *testing.T) {
	script := DefaultScript("127.0.0.1:9999")
	for _, want := range []string{
		"function FindProxyForURL(url, host)",
		"isPlainHostName(host)",
		"dnsResolve(host)",
		"isInNet(ip, \"10.0.0.0\", \"255.0.0.0\")",
		"isInNet(ip, \"172.16.0.0\", \"255.240.0.0\")",
		"isInNet(ip, \"192.168.0.0\", \"255.255.0.0\")",
		"isInNet(ip, \"169.254.0.0\", \"255.255.0.0\")",
		"PROXY 127.0.0.1:9999",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("default script missing %q", want)
		}
	}
	// No silent DIRECT fallback after the PROXY line — a dead core must
	// fail traffic, not leak it.
	if strings.Contains(script, "; DIRECT") {
		t.Error("default script must not fall back to DIRECT after PROXY")
	}
}
