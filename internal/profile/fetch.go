package profile

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FetchResponse carries subscription body plus panel metadata.
type FetchResponse struct {
	Body        string
	ContentType string
	SubInfo     *SubscriptionInfo
}

var userinfoRe = regexp.MustCompile(`([a-z_]+)=([0-9]+)`)

// Fetcher downloads subscription content.
//
// DESIGN: Strategy — ProxyFunc decides whether the request goes direct or
// through the running mixed port; the engine injects the proxy-aware
// implementation, keeping this package free of engine dependencies.
type Fetcher struct {
	// Client is the HTTP client used for subscription downloads.
	Client *http.Client
	// MaxBytes caps the response size (default 32 MiB).
	MaxBytes int64
}

// NewFetcher builds a fetcher; proxyURL may be "" for direct connections.
func NewFetcher(proxyURL string, timeout time.Duration) *Fetcher {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tr := &http.Transport{
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  false,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	if proxyURL != "" {
		tr.Proxy = http.ProxyURL(mustParseURL(proxyURL))
	}
	return &Fetcher{
		Client:   &http.Client{Timeout: timeout, Transport: tr},
		MaxBytes: 32 << 20,
	}
}

func mustParseURL(u string) *url.URL {
	parsed, _ := url.Parse(u)
	return parsed
}

// Fetch downloads a subscription URL and parses panel metadata.
func (f *Fetcher) Fetch(ctx context.Context, url string) (*FetchResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("bad url: %w", err)
	}
	// UA matters: many panels serve different formats per client.
	req.Header.Set("User-Agent", "NeuroRift/1.0 (mihomo; clash.meta compatible)")
	req.Header.Set("Accept", "*/*")

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.MaxBytes))
	if err != nil {
		return nil, fmt.Errorf("fetch body: %w", err)
	}
	return &FetchResponse{
		Body:        string(body),
		ContentType: resp.Header.Get("Content-Type"),
		SubInfo:     parseSubscriptionUserinfo(resp.Header.Get("Subscription-Userinfo")),
	}, nil
}

// parseSubscriptionUserinfo parses "upload=123; download=456; total=789; expire=1750000000".
func parseSubscriptionUserinfo(h string) *SubscriptionInfo {
	if h == "" {
		return nil
	}
	info := &SubscriptionInfo{}
	for _, m := range userinfoRe.FindAllStringSubmatch(h, -1) {
		v, _ := strconv.ParseInt(m[2], 10, 64)
		switch m[1] {
		case "upload":
			info.Upload = v
		case "download":
			info.Download = v
		case "total":
			info.Total = v
		case "expire":
			info.ExpireAt = time.Unix(v, 0)
		}
	}
	if info.Total == 0 && info.ExpireAt.IsZero() {
		return nil
	}
	return info
}

// HostOf extracts the host portion of a URL for display.
func HostOf(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 { // strip userinfo
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 { // strip port (naive, IPv6 ok enough for display)
		if !strings.HasPrefix(s, "[") {
			s = s[:i]
		} else if j := strings.Index(s, "]"); j >= 0 {
			s = s[1:j]
		}
	}
	return s
}
