package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/neurorift/neurorift/internal/yamlx"
)

// parseShareLink converts a single proxy sharing URI into a Node.
// Supported schemes: vmess, vless, trojan, ss, ssr, hysteria2/hy2, tuic,
// anytls, socks5/socks, http/https.
//
// Builders emit *yamlx.OMap in mihomo's proxy schema with a stable,
// human-readable key order: name, type, server, port, auth, transport, tls.
func parseShareLink(raw string) (*Node, error) {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "vmess://"):
		return parseVMess(raw)
	case strings.HasPrefix(lower, "vless://"):
		return parseVLESS(raw)
	case strings.HasPrefix(lower, "trojan://"):
		return parseTrojan(raw)
	case strings.HasPrefix(lower, "ss://"):
		return parseSS(raw)
	case strings.HasPrefix(lower, "ssr://"):
		return parseSSR(raw)
	case strings.HasPrefix(lower, "hysteria2://"), strings.HasPrefix(lower, "hy2://"):
		return parseHysteria2(raw)
	case strings.HasPrefix(lower, "tuic://"):
		return parseTUIC(raw)
	case strings.HasPrefix(lower, "anytls://"):
		return parseAnyTLS(raw)
	case strings.HasPrefix(lower, "socks5://"), strings.HasPrefix(lower, "socks://"), strings.HasPrefix(lower, "socks5s://"):
		return parseSocks(raw)
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"), strings.HasPrefix(lower, "h2://"):
		// NB: plain http(s):// is only treated as a proxy when it appears in
		// subscription content (which is how v2rayN/Happ encode http nodes);
		// it never conflicts with subscription URLs, which are fetched via
		// Fetch, not parseShareLink.
		return parseHTTPProxy(raw)
	default:
		return nil, fmt.Errorf("unsupported scheme: %s", schemeOf(raw))
	}
}

func schemeOf(raw string) string {
	if i := strings.Index(raw, "://"); i > 0 {
		return raw[:i]
	}
	return ""
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// parseHostPort extracts host/port, handling bracketed IPv6 literals.
func parseHostPort(host, portStr string, defaultPort int) (string, int, error) {
	host = strings.Trim(host, "[]")
	if host == "" {
		return "", 0, errors.New("empty host")
	}
	port := defaultPort
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1 || p > 65535 {
			return "", 0, fmt.Errorf("invalid port %q", portStr)
		}
		port = p
	}
	if port == 0 {
		return "", 0, errors.New("missing port")
	}
	return host, port, nil
}

func validServer(server string) error {
	if server == "" {
		return errors.New("empty server")
	}
	if net.ParseIP(strings.Trim(server, "[]")) == nil && !strings.Contains(strings.TrimSuffix(server, "."), ".") {
		// Allow single-label hostnames (LAN setups) but reject spaces etc.
		if strings.ContainsAny(server, " \t/") {
			return fmt.Errorf("invalid server %q", server)
		}
	}
	return nil
}

// truthy interprets common boolean encodings used by sharing links.
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y":
		return true
	}
	return false
}

func splitComma(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// applyTLS adds the common TLS block fields to m. sniKey selects the field
// name mihomo uses per protocol ("servername" for vmess/vless, "sni" for
// trojan).
func applyTLS(m *yamlx.OMap, tlsEnabled bool, sniKey, sni string, alpn []string, insecure bool, fp string) {
	if tlsEnabled {
		m.Set("tls", true)
	}
	if sni != "" {
		m.Set(sniKey, sni)
	}
	if insecure {
		m.Set("skip-cert-verify", true)
	}
	if fp != "" {
		m.Set("client-fingerprint", fp)
	}
	if len(alpn) > 0 {
		m.Set("alpn", alpn)
	}
}

// applyNetwork emits the transport block for ws / grpc / h2 / httpupgrade /
// splithttp(xhttp) networks. Returns the mihomo network name.
func applyNetwork(m *yamlx.OMap, network, path, host, serviceName string) string {
	network = strings.ToLower(network)
	switch network {
	case "", "tcp", "raw":
		return "tcp"
	case "ws", "websocket":
		opts := newOMap().Set("path", orDefault(path, "/"))
		if host != "" {
			opts.Set("headers", newOMap().Set("Host", host))
		}
		m.Set("network", "ws").Set("ws-opts", opts)
		return "ws"
	case "grpc":
		opts := newOMap()
		if serviceName != "" {
			opts.Set("grpc-service-name", serviceName)
		} else if path != "" {
			opts.Set("grpc-service-name", path)
		}
		m.Set("network", "grpc").Set("grpc-opts", opts)
		return "grpc"
	case "h2", "http2":
		opts := newOMap()
		if host != "" {
			opts.Set("host", []string{host})
		}
		opts.Set("path", orDefault(path, "/"))
		m.Set("network", "h2").Set("h2-opts", opts)
		return "h2"
	case "httpupgrade":
		opts := newOMap().Set("path", orDefault(path, "/"))
		if host != "" {
			opts.Set("host", host)
		}
		m.Set("network", "httpupgrade").Set("httpupgrade-opts", opts)
		return "httpupgrade"
	case "xhttp", "splithttp":
		opts := newOMap().Set("path", orDefault(path, "/"))
		if host != "" {
			opts.Set("host", host)
		}
		m.Set("network", "splithttp").Set("splithttp-opts", opts)
		return "splithttp"
	case "http":
		// TCP with HTTP obfs header (v2ray "tcp + headerType=http").
		opts := newOMap().Set("path", []string{orDefault(path, "/")})
		if host != "" {
			opts.Set("headers", newOMap().Set("Host", []string{host}))
		}
		m.Set("network", "http").Set("http-opts", opts)
		return "http"
	default:
		// mkcp / quic are not supported by mihomo: emit as-is; the core
		// will reject it and the UI surfaces the node as unavailable.
		m.Set("network", network)
		return network
	}
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func fallbackName(prefix, host string, port int) string {
	return fmt.Sprintf("%s-%s:%d", prefix, host, port)
}

// ---------------------------------------------------------------------------
// vmess://  (base64 JSON, v2rayN format)
// ---------------------------------------------------------------------------

type vmessLink struct {
	V    any    `json:"v"`
	PS   string `json:"ps"`
	Add  string `json:"add"`
	Port any    `json:"port"`
	ID   string `json:"id"`
	AID  any    `json:"aid"`
	SCY  string `json:"scy"`
	Net  string `json:"net"`
	Type string `json:"type"`
	Host string `json:"host"`
	Path string `json:"path"`
	TLS  string `json:"tls"`
	SNI  string `json:"sni"`
	ALPN string `json:"alpn"`
	FP   string `json:"fp"`
}

func parseVMess(raw string) (*Node, error) {
	payload := raw[len("vmess://"):]
	// Some clients URI-encode the base64 payload; tolerate both.
	if unescaped, err := url.QueryUnescape(payload); err == nil {
		payload = unescaped
	}
	data, ok := decodeB64(payload)
	if !ok {
		return nil, errors.New("vmess: invalid base64 payload")
	}
	var v vmessLink
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("vmess: bad json: %w", err)
	}
	if v.Add == "" || v.ID == "" {
		return nil, errors.New("vmess: missing server or uuid")
	}
	port := anyToInt(v.Port, 0)
	if port == 0 {
		return nil, errors.New("vmess: missing port")
	}
	if err := validServer(v.Add); err != nil {
		return nil, err
	}

	cipher := v.SCY
	if cipher == "" {
		cipher = "auto"
	}
	alterID := anyToInt(v.AID, 0)

	m := newOMap().
		Set("name", orDefault(v.PS, fallbackName("vmess", v.Add, port))).
		Set("type", "vmess").
		Set("server", v.Add).
		Set("port", port).
		Set("uuid", v.ID).
		Set("alterId", alterID).
		Set("cipher", cipher).
		Set("udp", true)

	tlsEnabled := v.TLS == "tls" || truthy(v.TLS)
	// v2rayN encodes TCP + HTTP obfs header as net=tcp & type=http
	// (#102 round-trip); other clients put "none" in type.
	net := v.Net
	if (net == "" || net == "tcp") && strings.EqualFold(v.Type, "http") {
		net = "http"
	}
	applyNetwork(m, net, v.Path, v.Host, "")
	applyTLS(m, tlsEnabled, "servername", firstNonEmpty(v.SNI, v.Host), splitComma(v.ALPN), false, v.FP)

	name, _ := m.Get("name")
	return &Node{Name: str(name), Raw: m}, nil
}

func anyToInt(v any, def int) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	}
	return def
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// ---------------------------------------------------------------------------
// vless://  (Xray URL format, incl. REALITY)
// ---------------------------------------------------------------------------

func parseVLESS(raw string) (*Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("vless: %w", err)
	}
	q := u.Query()
	host, port, err := parseHostPort(u.Hostname(), u.Port(), 443)
	if err != nil {
		return nil, fmt.Errorf("vless: %w", err)
	}
	if err := validServer(host); err != nil {
		return nil, err
	}
	uuid := u.User.Username()
	if uuid == "" {
		return nil, errors.New("vless: missing uuid")
	}

	security := strings.ToLower(q.Get("security"))
	insecure := truthy(firstNonEmpty(q.Get("allowInsecure"), q.Get("insecure"))) || q.Get("verify") == "false"
	sni := firstNonEmpty(q.Get("sni"), q.Get("peer"), q.Get("host"))
	fp := q.Get("fp")
	alpn := splitComma(q.Get("alpn"))

	name := orDefault(u.Fragment, fallbackName("vless", host, port))
	m := newOMap().
		Set("name", name).
		Set("type", "vless").
		Set("server", host).
		Set("port", port).
		Set("uuid", uuid).
		Set("udp", true)

	tlsEnabled := security == "tls" || security == "reality"
	if security == "reality" {
		ro := newOMap()
		if pbk := q.Get("pbk"); pbk != "" {
			ro.Set("public-key", pbk)
		}
		if sid := q.Get("sid"); sid != "" {
			ro.Set("short-id", sid)
		}
		if ro.Len() > 0 {
			m.Set("reality-opts", ro)
		}
		// REALITY requires a client fingerprint.
		if fp == "" {
			fp = "chrome"
		}
	}
	if flow := q.Get("flow"); flow != "" {
		m.Set("flow", flow)
	}
	if pe := q.Get("packetEncoding"); pe != "" {
		m.Set("packet-encoding", pe)
	}
	// Post-quantum VLESS encryption (Xray's new VLESS encryption, supported
	// by mihomo >= v1.19.27): the dot-separated share-link value IS the mihomo
	// field value — panels emit the identical string in the Clash YAML they
	// serve to meta clients. The legacy "none" default is omitted (issue #90).
	if enc := q.Get("encryption"); enc != "" && enc != "none" {
		m.Set("encryption", enc)
	}

	net := applyNetwork(m, q.Get("type"), q.Get("path"), q.Get("host"), q.Get("serviceName"))
	if net == "tcp" && q.Get("headerType") == "http" {
		// tcp + headerType=http maps to mihomo's http network.
		httpOpts := newOMap().Set("path", []string{orDefault(q.Get("path"), "/")})
		if h := q.Get("host"); h != "" {
			httpOpts.Set("headers", newOMap().Set("Host", []string{h}))
		}
		m.Set("network", "http").Set("http-opts", httpOpts)
	}
	applyTLS(m, tlsEnabled, "servername", sni, alpn, insecure, fp)
	return &Node{Name: name, Raw: m}, nil
}

// ---------------------------------------------------------------------------
// trojan://
// ---------------------------------------------------------------------------

func parseTrojan(raw string) (*Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("trojan: %w", err)
	}
	q := u.Query()
	host, port, err := parseHostPort(u.Hostname(), u.Port(), 443)
	if err != nil {
		return nil, fmt.Errorf("trojan: %w", err)
	}
	if err := validServer(host); err != nil {
		return nil, err
	}
	password := u.User.Username()
	if pw, ok := u.User.Password(); ok && pw != "" {
		password = password + ":" + pw // rare but some links split at ":"
	}
	if password == "" {
		return nil, errors.New("trojan: missing password")
	}

	insecure := truthy(firstNonEmpty(q.Get("allowInsecure"), q.Get("insecure")))
	security := strings.ToLower(q.Get("security"))
	tlsEnabled := security != "none" // trojan is TLS by default

	name := orDefault(u.Fragment, fallbackName("trojan", host, port))
	m := newOMap().
		Set("name", name).
		Set("type", "trojan").
		Set("server", host).
		Set("port", port).
		Set("password", password).
		Set("udp", true)

	if security == "reality" {
		ro := newOMap()
		if pbk := q.Get("pbk"); pbk != "" {
			ro.Set("public-key", pbk)
		}
		if sid := q.Get("sid"); sid != "" {
			ro.Set("short-id", sid)
		}
		if ro.Len() > 0 {
			m.Set("reality-opts", ro)
		}
	}
	applyNetwork(m, q.Get("type"), q.Get("path"), q.Get("host"), q.Get("serviceName"))
	applyTLS(m, tlsEnabled, "sni", firstNonEmpty(q.Get("sni"), q.Get("peer"), q.Get("host")),
		splitComma(q.Get("alpn")), insecure, q.Get("fp"))
	return &Node{Name: name, Raw: m}, nil
}

// ---------------------------------------------------------------------------
// ss://  (Shadowsocks, incl. SIP002 and legacy formats + plugins)
// ---------------------------------------------------------------------------

func parseSS(raw string) (*Node, error) {
	body := raw[len("ss://"):]
	var fragment string
	if i := strings.LastIndex(body, "#"); i >= 0 {
		fragment, _ = url.PathUnescape(body[i+1:])
		body = body[:i]
	}
	// Strip query (plugin params) but keep it.
	var query url.Values
	if i := strings.Index(body, "?"); i >= 0 {
		if q, err := url.ParseQuery(body[i+1:]); err == nil {
			query = q
		}
		body = body[:i]
	}

	var method, password, host, portStr string
	if at := strings.LastIndex(body, "@"); at >= 0 {
		// SIP002: ss://base64(method:password)@host:port
		userInfo := body[:at]
		if decoded, ok := decodeB64(userInfo); ok {
			userInfo = string(decoded)
		} else if unesc, err := url.QueryUnescape(userInfo); err == nil {
			userInfo = unesc
		}
		if i := strings.Index(userInfo, ":"); i >= 0 {
			method, password = userInfo[:i], userInfo[i+1:]
		} else {
			method = userInfo
		}
		hostport := body[at+1:]
		var err error
		host, portStr, err = splitHostPort(hostport)
		if err != nil {
			return nil, fmt.Errorf("ss: %w", err)
		}
	} else {
		// Legacy: ss://base64(method:password@host:port)
		decoded, ok := decodeB64(body)
		if !ok {
			return nil, errors.New("ss: undecodable payload")
		}
		s := string(decoded)
		at := strings.LastIndex(s, "@")
		if at < 0 {
			return nil, errors.New("ss: malformed legacy payload")
		}
		userInfo := s[:at]
		var err error
		host, portStr, err = splitHostPort(s[at+1:])
		if err != nil {
			return nil, fmt.Errorf("ss: %w", err)
		}
		if i := strings.Index(userInfo, ":"); i >= 0 {
			method, password = userInfo[:i], userInfo[i+1:]
		}
	}

	if method == "" {
		return nil, errors.New("ss: missing cipher")
	}
	host, port, err := parseHostPort(host, portStr, 0)
	if err != nil {
		return nil, fmt.Errorf("ss: %w", err)
	}
	if err := validServer(host); err != nil {
		return nil, err
	}

	name := orDefault(fragment, fallbackName("ss", host, port))
	m := newOMap().
		Set("name", name).
		Set("type", "ss").
		Set("server", host).
		Set("port", port).
		Set("cipher", method).
		Set("password", password).
		Set("udp", true)

	if plugin := query.Get("plugin"); plugin != "" {
		applySSPlugin(m, plugin)
	}
	return &Node{Name: name, Raw: m}, nil
}

// applySSPlugin translates the v2rayN plugin expression into mihomo fields.
func applySSPlugin(m *yamlx.OMap, plugin string) {
	parts := strings.Split(plugin, ";")
	name := strings.TrimSpace(parts[0])
	opts := map[string]string{}
	bareTLS := false // v2rayN writes the v2ray-plugin TLS flag as a bare ";tls" token
	for _, p := range parts[1:] {
		if strings.TrimSpace(p) == "tls" {
			bareTLS = true
			continue
		}
		if i := strings.Index(p, "="); i > 0 {
			opts[strings.TrimSpace(p[:i])] = strings.TrimSpace(p[i+1:])
		}
	}
	switch name {
	case "obfs-local", "simple-obfs":
		po := newOMap().Set("mode", orDefault(opts["obfs"], "http"))
		if h := opts["obfs-host"]; h != "" {
			po.Set("host", h)
		}
		m.Set("plugin", "obfs").Set("plugin-opts", po)
	case "v2ray-plugin":
		po := newOMap().Set("mode", orDefault(opts["mode"], "websocket"))
		if tls, ok := opts["tls"]; ok && truthy(tls) {
			po.Set("tls", true)
		} else if bareTLS {
			po.Set("tls", true)
		}
		if h := opts["host"]; h != "" {
			po.Set("host", h)
		}
		if p := opts["path"]; p != "" {
			po.Set("path", p)
		}
		m.Set("plugin", "v2ray-plugin").Set("plugin-opts", po)
	default:
		// Unknown plugin: keep transportless (node may still work if the
		// server runs without obfs) and surface via warnings elsewhere.
	}
}

// splitHostPort handles bracketed IPv6 and bare host:port.
func splitHostPort(s string) (string, string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return "", "", errors.New("unterminated IPv6 literal")
		}
		host := s[1:end]
		rest := s[end+1:]
		if !strings.HasPrefix(rest, ":") {
			return host, "", nil
		}
		return host, rest[1:], nil
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[:i], s[i+1:], nil
	}
	return s, "", nil
}

// ---------------------------------------------------------------------------
// ssr://  (ShadowsocksR)
// ---------------------------------------------------------------------------

func parseSSR(raw string) (*Node, error) {
	payload := raw[len("ssr://"):]
	data, ok := decodeB64(payload)
	if !ok {
		return nil, errors.New("ssr: undecodable payload")
	}
	s := string(data)
	main := s
	var params url.Values
	if i := strings.Index(s, "/?"); i >= 0 {
		main = s[:i]
		if q, err := url.ParseQuery(s[i+2:]); err == nil {
			params = q
		}
	}
	parts := strings.Split(main, ":")
	if len(parts) < 6 {
		return nil, errors.New("ssr: malformed payload")
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("ssr: bad port: %w", err)
	}
	host := parts[0]
	passwordB64 := parts[5]
	password := ""
	if b, ok := decodeB64(passwordB64); ok {
		password = string(b)
	}
	if err := validServer(host); err != nil {
		return nil, err
	}

	decodeParam := func(key string) string {
		if v := params.Get(key); v != "" {
			if b, ok := decodeB64(v); ok {
				return string(b)
			}
		}
		return ""
	}
	name := decodeParam("remarks")
	if name == "" {
		name = fallbackName("ssr", host, port)
	}

	m := newOMap().
		Set("name", name).
		Set("type", "ssr").
		Set("server", host).
		Set("port", port).
		Set("cipher", parts[3]).
		Set("password", password).
		Set("protocol", parts[2]).
		Set("obfs", parts[4]).
		Set("udp", true)
	if pp := decodeParam("protoparam"); pp != "" {
		m.Set("protocol-param", pp)
	}
	if op := decodeParam("obfsparam"); op != "" {
		m.Set("obfs-param", op)
	}
	return &Node{Name: name, Raw: m}, nil
}

// ---------------------------------------------------------------------------
// hysteria2:// / hy2://
// ---------------------------------------------------------------------------

func parseHysteria2(raw string) (*Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("hysteria2: %w", err)
	}
	q := u.Query()
	host, port, err := parseHostPort(u.Hostname(), u.Port(), 443)
	if err != nil {
		return nil, fmt.Errorf("hysteria2: %w", err)
	}
	if err := validServer(host); err != nil {
		return nil, err
	}
	auth := ""
	if u.User != nil {
		auth = u.User.Username()
		if pw, ok := u.User.Password(); ok && pw != "" {
			auth = auth + ":" + pw
		}
	}

	name := orDefault(u.Fragment, fallbackName("hy2", host, port))
	m := newOMap().
		Set("name", name).
		Set("type", "hysteria2").
		Set("server", host).
		Set("port", port).
		Set("password", auth).
		Set("udp", true)

	// Port hopping: "mport" / "ports" e.g. "443,8000-9000".
	if ports := firstNonEmpty(q.Get("mport"), q.Get("ports")); ports != "" {
		m.Set("ports", ports)
	}
	if obfs := q.Get("obfs"); obfs != "" {
		m.Set("obfs", obfs)
		if opw := q.Get("obfs-password"); opw != "" {
			m.Set("obfs-password", opw)
		}
	}
	if sni := firstNonEmpty(q.Get("sni"), q.Get("peer")); sni != "" {
		m.Set("sni", sni)
	}
	if truthy(firstNonEmpty(q.Get("insecure"), q.Get("allowInsecure"), q.Get("allow-insecure"))) {
		m.Set("skip-cert-verify", true)
	}
	if pin := firstNonEmpty(q.Get("pinSHA256"), q.Get("pinned-certchain-sha256")); pin != "" {
		m.Set("fingerprint", pin)
	}
	return &Node{Name: name, Raw: m}, nil
}

// ---------------------------------------------------------------------------
// tuic://  (v5 uuid:password and v4 token)
// ---------------------------------------------------------------------------

func parseTUIC(raw string) (*Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("tuic: %w", err)
	}
	q := u.Query()
	host, port, err := parseHostPort(u.Hostname(), u.Port(), 443)
	if err != nil {
		return nil, fmt.Errorf("tuic: %w", err)
	}
	if err := validServer(host); err != nil {
		return nil, err
	}

	name := orDefault(u.Fragment, fallbackName("tuic", host, port))
	m := newOMap().
		Set("name", name).
		Set("type", "tuic").
		Set("server", host).
		Set("port", port).
		Set("udp", true)

	user := u.User.Username()
	pass, hasPass := u.User.Password()
	if hasPass {
		m.Set("uuid", user).Set("password", pass)
	} else {
		m.Set("token", user) // TUIC v4
	}
	if cc := firstNonEmpty(q.Get("congestion_control"), q.Get("congestion-controller")); cc != "" {
		m.Set("congestion-controller", cc)
	}
	if ur := firstNonEmpty(q.Get("udp_relay_mode"), q.Get("udp-relay-mode")); ur != "" {
		m.Set("udp-relay-mode", ur)
	}
	if sni := firstNonEmpty(q.Get("sni"), q.Get("peer")); sni != "" {
		m.Set("sni", sni)
	}
	if alpn := splitComma(q.Get("alpn")); len(alpn) > 0 {
		m.Set("alpn", alpn)
	}
	if truthy(firstNonEmpty(q.Get("allow_insecure"), q.Get("insecure"), q.Get("allowInsecure"))) {
		m.Set("skip-cert-verify", true)
	}
	return &Node{Name: name, Raw: m}, nil
}

// ---------------------------------------------------------------------------
// anytls://
// ---------------------------------------------------------------------------

func parseAnyTLS(raw string) (*Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("anytls: %w", err)
	}
	q := u.Query()
	host, port, err := parseHostPort(u.Hostname(), u.Port(), 443)
	if err != nil {
		return nil, fmt.Errorf("anytls: %w", err)
	}
	if err := validServer(host); err != nil {
		return nil, err
	}
	password := u.User.Username()
	if password == "" {
		return nil, errors.New("anytls: missing password")
	}
	name := orDefault(u.Fragment, fallbackName("anytls", host, port))
	m := newOMap().
		Set("name", name).
		Set("type", "anytls").
		Set("server", host).
		Set("port", port).
		Set("password", password).
		Set("udp", true)
	if sni := firstNonEmpty(q.Get("sni"), q.Get("peer")); sni != "" {
		m.Set("sni", sni)
	}
	if truthy(firstNonEmpty(q.Get("insecure"), q.Get("allowInsecure"))) {
		m.Set("skip-cert-verify", true)
	}
	if fp := q.Get("fp"); fp != "" {
		m.Set("client-fingerprint", fp)
	}
	if alpn := splitComma(q.Get("alpn")); len(alpn) > 0 {
		m.Set("alpn", alpn)
	}
	return &Node{Name: name, Raw: m}, nil
}

// ---------------------------------------------------------------------------
// socks5:// and http(s):// proxies
// ---------------------------------------------------------------------------

func parseSocks(raw string) (*Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("socks: %w", err)
	}
	host, port, err := parseHostPort(u.Hostname(), u.Port(), 1080)
	if err != nil {
		return nil, fmt.Errorf("socks: %w", err)
	}
	if err := validServer(host); err != nil {
		return nil, err
	}
	name := orDefault(u.Fragment, fallbackName("socks", host, port))
	m := newOMap().
		Set("name", name).
		Set("type", "socks5").
		Set("server", host).
		Set("port", port).
		Set("udp", true)
	if user := u.User.Username(); user != "" {
		m.Set("username", user)
		if pass, ok := u.User.Password(); ok {
			m.Set("password", pass)
		}
	}
	if strings.HasPrefix(strings.ToLower(raw), "socks5s://") {
		m.Set("tls", true)
	}
	return &Node{Name: name, Raw: m}, nil
}

func parseHTTPProxy(raw string) (*Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	host, port, err := parseHostPort(u.Hostname(), u.Port(), 80)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	if err := validServer(host); err != nil {
		return nil, err
	}
	name := orDefault(u.Fragment, fallbackName("http", host, port))
	m := newOMap().
		Set("name", name).
		Set("type", "http").
		Set("server", host).
		Set("port", port)
	if user := u.User.Username(); user != "" {
		m.Set("username", user)
		if pass, ok := u.User.Password(); ok {
			m.Set("password", pass)
		}
	}
	if strings.EqualFold(u.Scheme, "https") || strings.EqualFold(u.Scheme, "h2") {
		m.Set("tls", true)
	}
	return &Node{Name: name, Raw: m}, nil
}
