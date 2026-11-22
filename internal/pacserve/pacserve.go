// Package pacserve serves the PAC (Proxy Auto-Config) file on a loopback
// HTTP listener (issue #15).
//
// PAC mode is the third proxy mode next to Off / System Proxy: instead of
// pointing WinINet at the mixed port directly, the OS fetches this PAC file
// and evaluates FindProxyForURL per request — LAN and private ranges stay
// DIRECT while everything else goes through the core. The listener binds
// 127.0.0.1 only and the content hot-reloads via SetContent, so editing the
// script in Settings takes effect without touching the OS proxy settings.
//
// AI-DEVELOPER NOTES:
//   - The server never touches the registry; platform.SetPACAutoconfig
//     owns the WinINet AutoConfigURL switch (same split as SetSystemProxy).
//   - Start(port) returns the ACTUAL bound port: callers pass the configured
//     port and fall back to 0 (ephemeral) when it is taken by another app.
//   - No graceful-shutdown timeouts beyond 2s — the listener dies with the
//     process anyway; Stop is idempotent and safe to defer everywhere.
package pacserve

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ContentType is the MIME type Windows expects for PAC files.
const ContentType = "application/x-ns-proxy-autoconfig"

// Path is the only route served.
const Path = "/pac"

// Server is the loopback PAC file listener. All public methods are safe for
// concurrent use.
type Server struct {
	log *slog.Logger

	mu      sync.Mutex
	content []byte
	ln      net.Listener
	hs      *http.Server
	port    int
}

// New builds an idle server.
func New(log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{log: log}
}

// Start binds 127.0.0.1:<port> and begins serving. port 0 picks an
// ephemeral port. Returns the actual bound port. Starting an already
// running server is an error — Stop it first (the orchestration layer
// restarts on port changes).
func (s *Server) Start(port int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return s.port, fmt.Errorf("pac listener already running on port %d", s.port)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
	if err != nil {
		return 0, fmt.Errorf("pac listen on 127.0.0.1:%d: %w", port, err)
	}
	s.ln = ln
	s.port = ln.Addr().(*net.TCPAddr).Port
	hs := &http.Server{
		Handler:           http.HandlerFunc(s.handle),
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.hs = hs
	go func(ln net.Listener, port int) {
		if err := hs.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.log.Warn("pac listener stopped unexpectedly", "port", port, "err", err)
		}
	}(ln, s.port)
	s.log.Info("pac listener up", "port", s.port)
	return s.port, nil
}

// Stop shuts the listener down (idempotent; nil when not running).
func (s *Server) Stop() error {
	s.mu.Lock()
	hs, ln := s.hs, s.ln
	s.hs, s.ln, s.port = nil, nil, 0
	s.mu.Unlock()
	if hs == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := hs.Shutdown(ctx); err != nil {
		_ = ln.Close()
		return err
	}
	return nil
}

// SetContent hot-reloads the served PAC file (the next request picks it up).
func (s *Server) SetContent(content []byte) {
	s.mu.Lock()
	s.content = content
	s.mu.Unlock()
}

// Port returns the actual bound port (0 when not running).
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// Running reports whether the listener is up.
func (s *Server) Running() bool { return s.Port() != 0 }

// URL returns the AutoConfigURL target for the running listener
// ("" when not running).
func (s *Server) URL() string {
	p := s.Port()
	if p == 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d%s", p, Path)
}

// handle serves GET/HEAD /pac with the current content.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != Path {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	content := s.content
	s.mu.Unlock()
	if content == nil {
		content = []byte(DefaultScript("127.0.0.1:7897"))
	}
	// no-store: the OS re-fetches the PAC periodically and after every
	// WinINet refresh; a stale intermediate cache would freeze old rules.
	w.Header().Set("Content-Type", ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(content)
	}
}

// DefaultScript renders the factory PAC file: private and link-local
// ranges stay DIRECT (NAS, router UI, printer, local services), plain
// hostnames stay DIRECT, everything else goes through the core's mixed
// port. No DIRECT fallback after the PROXY line — when the core is down
// traffic must fail, not silently bypass it.
func DefaultScript(proxyServer string) string {
	return strings.Join([]string{
		"// NeuroRift PAC — auto-generated default (issue #15).",
		"// Edit in Settings → Proxy mode. LAN / private ranges are DIRECT;",
		"// everything else goes through the NeuroRift mixed port.",
		"function FindProxyForURL(url, host) {",
		"    if (isPlainHostName(host)) return \"DIRECT\";",
		"    if (host === \"localhost\" || shExpMatch(host, \"*.local\")) return \"DIRECT\";",
		"    var ip = dnsResolve(host);",
		"    if (ip) {",
		"        if (isInNet(ip, \"127.0.0.0\", \"255.0.0.0\")) return \"DIRECT\";",
		"        if (isInNet(ip, \"10.0.0.0\", \"255.0.0.0\")) return \"DIRECT\";",
		"        if (isInNet(ip, \"172.16.0.0\", \"255.240.0.0\")) return \"DIRECT\";",
		"        if (isInNet(ip, \"192.168.0.0\", \"255.255.0.0\")) return \"DIRECT\";",
		"        if (isInNet(ip, \"169.254.0.0\", \"255.255.0.0\")) return \"DIRECT\";",
		"    }",
		"    return \"PROXY " + proxyServer + "\";",
		"}",
		"",
	}, "\n")
}
