// Package engine runs and supervises the mihomo core, generates its runtime
// configuration and exposes a typed client for the core's external
// controller API.
//
// COMPONENT MAP (see docs/ARCHITECTURE.md for the full diagram):
//
//	Manager      high-level lifecycle: EnsureCore -> GenerateConfig ->
//	             Runner.Start -> wait healthy -> start Streams.
//	CoreManager  downloads/updates the mihomo binary (stable|alpha channel,
//	             mirror fallbacks, compatible-CPU fallback).
//	Runner       process supervision with restart backoff + crash-loop guard.
//	ConfigGen    merges settings + active profile into the runtime YAML.
//	API          typed REST client for the external controller.
//	Streams      streaming consumers (traffic / logs) + connection polling.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// API is the typed client for mihomo's external controller.
// It is safe for concurrent use.
type API struct {
	base   string // http://127.0.0.1:<port>
	secret string
	client *http.Client
	// streamClient serves the never-ending endpoints (/traffic, /memory,
	// /logs). It has NO overall Timeout: http.Client.Timeout covers the
	// whole request including body reads and would kill every stream 30 s
	// in (issue #39). Liveness is bounded by the dialer + TLS handshake
	// timeouts and the caller's context instead.
	streamClient *http.Client
}

// NewAPI builds a client for the local controller.
func NewAPI(port int, secret string) *API {
	transport := &http.Transport{
		// Local core: no keep-alive dance needed, but bound the phases that
		// could otherwise hang forever on a wedged core.
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          8,
		IdleConnTimeout:       90 * time.Second,
	}
	return &API{
		base:         fmt.Sprintf("http://127.0.0.1:%d", port),
		secret:       secret,
		client:       &http.Client{Timeout: 30 * time.Second, Transport: transport},
		streamClient: &http.Client{Transport: transport.Clone()}, // no Timeout — see field doc
	}
}

// --- model types (subset of mihomo's API schema that we consume) ----------

// DelayHistory is one latency sample.
type DelayHistory struct {
	Time  string `json:"time"`
	Delay int    `json:"delay"`
}

// Proxy is a proxy or group as reported by GET /proxies.
type Proxy struct {
	Name    string         `json:"name"`
	Type    string         `json:"type"` // Selector, URLTest, Fallback, LoadBalance, Vmess...
	Now     string         `json:"now"`
	All     []string       `json:"all"`
	History []DelayHistory `json:"history"`
	UDP     bool           `json:"udp"`
	Alive   bool           `json:"alive"`
}

// proxiesEnvelope wraps GET /proxies.
type proxiesEnvelope struct {
	Proxies map[string]Proxy `json:"proxies"`
}

// Connection is one live connection from GET /connections.
type Connection struct {
	ID       string   `json:"id"`
	Upload   int64    `json:"upload"`
	Download int64    `json:"download"`
	Start    string   `json:"start"`
	Rule     string   `json:"rule"`
	Chains   []string `json:"chains"`
	Metadata struct {
		Network         string `json:"network"`
		Type            string `json:"type"`
		SourceIP        string `json:"sourceIP"`
		DestinationIP   string `json:"destinationIP"`
		DestinationPort string `json:"destinationPort"`
		Host            string `json:"host"`
		Process         string `json:"process"`
		ProcessPath     string `json:"processPath"`
	} `json:"metadata"`
}

// connectionsEnvelope wraps GET /connections.
type connectionsEnvelope struct {
	DownloadTotal int64        `json:"downloadTotal"`
	UploadTotal   int64        `json:"uploadTotal"`
	Connections   []Connection `json:"connections"`
}

// Rule is one routing rule from GET /rules.
type Rule struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
	Proxy   string `json:"proxy"`
}

// ProviderInfo describes a proxy provider from GET /providers/proxies.
type ProviderInfo struct {
	Name        string  `json:"name"`
	VehicleType string  `json:"vehicleType"` // File / HTTP ...
	UpdatedAt   string  `json:"updatedAt"`
	Proxies     []Proxy `json:"proxies"`
}

// ConfigInfo mirrors GET /configs.
type ConfigInfo struct {
	Mode string `json:"mode"`
	// Additional fields are intentionally ignored; the app only patches.
}

// --- low-level request helpers --------------------------------------------

func (a *API) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, rd)
	if err != nil {
		return err
	}
	if a.secret != "" {
		req.Header.Set("Authorization", "Bearer "+a.secret)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		msg := ""
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &e) == nil && e.Message != "" {
			msg = e.Message
		} else {
			msg = string(bytes.TrimSpace(data))
		}
		return &APIError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return nil
}

// APIError is a non-2xx controller response.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("mihomo api %d: %s", e.Status, e.Message)
}

// --- endpoints ---------------------------------------------------------------

// Version returns the running core version.
func (a *API) Version(ctx context.Context) (string, error) {
	var out struct {
		Version string `json:"version"`
	}
	if err := a.do(ctx, http.MethodGet, "/version", nil, &out); err != nil {
		return "", err
	}
	return out.Version, nil
}

// Proxies returns all proxies and groups keyed by name.
func (a *API) Proxies(ctx context.Context) (map[string]Proxy, error) {
	var out proxiesEnvelope
	if err := a.do(ctx, http.MethodGet, "/proxies", nil, &out); err != nil {
		return nil, err
	}
	return out.Proxies, nil
}

// SelectProxy switches a selector group to the given member.
func (a *API) SelectProxy(ctx context.Context, group, member string) error {
	body := map[string]string{"name": member}
	return a.do(ctx, http.MethodPut, "/proxies/"+url.PathEscape(group), body, nil)
}

// Delay tests one proxy through the core (real URL test).
func (a *API) Delay(ctx context.Context, proxy, testURL string, timeoutMS int) (int, error) {
	var out struct {
		Delay   int    `json:"delay"`
		Message string `json:"message"`
	}
	q := url.Values{}
	q.Set("timeout", strconv.Itoa(timeoutMS))
	q.Set("url", testURL)
	err := a.do(ctx, http.MethodGet, "/proxies/"+url.PathEscape(proxy)+"/delay?"+q.Encode(), nil, &out)
	if err != nil {
		return 0, err
	}
	if out.Message != "" && out.Delay == 0 {
		return 0, fmt.Errorf("%s", out.Message)
	}
	return out.Delay, nil
}

// GroupDelay tests every member of a group at once (single API call,
// core-side concurrency).
func (a *API) GroupDelay(ctx context.Context, group, testURL string, timeoutMS int) (map[string]int, error) {
	var out map[string]int
	q := url.Values{}
	q.Set("timeout", strconv.Itoa(timeoutMS))
	q.Set("url", testURL)
	// Failed members surface as {"name":{"error":...}} in some versions;
	// decode tolerantly into a raw map first.
	var raw map[string]json.RawMessage
	if err := a.do(ctx, http.MethodGet, "/group/"+url.PathEscape(group)+"/delay?"+q.Encode(), nil, &raw); err != nil {
		return nil, err
	}
	out = make(map[string]int, len(raw))
	for name, v := range raw {
		var d int
		if err := json.Unmarshal(v, &d); err == nil && d > 0 {
			out[name] = d
		}
	}
	return out, nil
}

// Connections returns the current connection snapshot.
func (a *API) Connections(ctx context.Context) (*connectionsEnvelope, error) {
	var out connectionsEnvelope
	if err := a.do(ctx, http.MethodGet, "/connections", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CloseConnection terminates one connection.
func (a *API) CloseConnection(ctx context.Context, id string) error {
	return a.do(ctx, http.MethodDelete, "/connections/"+url.PathEscape(id), nil, nil)
}

// CloseAllConnections terminates every connection.
func (a *API) CloseAllConnections(ctx context.Context) error {
	return a.do(ctx, http.MethodDelete, "/connections", nil, nil)
}

// Rules returns the active routing rules.
func (a *API) Rules(ctx context.Context) ([]Rule, error) {
	var out struct {
		Rules []Rule `json:"rules"`
	}
	if err := a.do(ctx, http.MethodGet, "/rules", nil, &out); err != nil {
		return nil, err
	}
	return out.Rules, nil
}

// Providers lists proxy providers.
func (a *API) Providers(ctx context.Context) (map[string]ProviderInfo, error) {
	var out struct {
		Providers map[string]ProviderInfo `json:"providers"`
	}
	if err := a.do(ctx, http.MethodGet, "/providers/proxies", nil, &out); err != nil {
		return nil, err
	}
	return out.Providers, nil
}

// UpdateProvider refreshes one provider from its source (for file providers
// this re-reads the file; for our subscriptions it is an instant hot-swap).
func (a *API) UpdateProvider(ctx context.Context, name string) error {
	return a.do(ctx, http.MethodPut, "/providers/proxies/"+url.PathEscape(name), nil, nil)
}

// PatchConfigs applies partial config changes (e.g. mode switching).
func (a *API) PatchConfigs(ctx context.Context, patch map[string]any) error {
	return a.do(ctx, http.MethodPatch, "/configs", patch, nil)
}

// ReloadConfigs applies a whole new config payload (hot reload; selections
// survive thanks to mihomo's store-selected profile cache).
func (a *API) ReloadConfigs(ctx context.Context, payload string) error {
	body := map[string]string{"payload": payload, "path": ""}
	return a.do(ctx, http.MethodPut, "/configs?force=true", body, nil)
}

// GetMode returns the current routing mode.
func (a *API) GetMode(ctx context.Context) (string, error) {
	var out ConfigInfo
	if err := a.do(ctx, http.MethodGet, "/configs", nil, &out); err != nil {
		return "", err
	}
	return out.Mode, nil
}

// SetMode switches routing mode (rule|global|direct) without a reload.
func (a *API) SetMode(ctx context.Context, mode string) error {
	return a.PatchConfigs(ctx, map[string]any{"mode": mode})
}

// StreamLine opens a chunked-JSON-lines endpoint (/traffic, /logs,
// /memory) and calls onLine for every decoded raw line. It returns when
// ctx is cancelled or the transport errors.
func (a *API) StreamLine(ctx context.Context, path string, onLine func(line []byte)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+path, nil)
	if err != nil {
		return err
	}
	if a.secret != "" {
		req.Header.Set("Authorization", "Bearer "+a.secret)
	}
	// streamClient (no client.Timeout) — a.Timeout would cut the body read
	// after 30 s and looked like "live traffic is dead" (issue #39).
	resp, err := a.streamClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return &APIError{Status: resp.StatusCode, Message: "stream " + path}
	}
	dec := json.NewDecoder(resp.Body)
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		onLine(raw)
	}
}
