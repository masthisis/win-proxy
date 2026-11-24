package profile

// uriexport.go serializes stored proxy maps back into the standard
// share-link formats — the inverse of uriparse.go (issue #102, owner
// directive 2026-11-24: v2ray.com awesome-tools client parity).
//
// Every scheme uriparse.go can parse also has an exporter here, so a node
// imported from a link can be re-shared later (v2rayN / Qv2ray / V2RayW
// all round-trip nodes this way). WireGuard nodes, which have no URI
// scheme, export as a standard wg-quick .conf (inverse of wgquick.go).
//
// Design rules:
//   - Exporters read the mihomo schema fields the parsers emit, nothing
//     else: a node that came in through a Clash YAML keeps whatever extra
//     fields it has — they are silently dropped from the link (share links
//     cannot carry them) but never from the stored node.
//   - Only lossless fields are exported; settings-injected defaults (UDP
//     override, smux, ...) live on the runtime config, not on the node.
//   - Every emitted link must re-parse to the same semantics (round-trip
//     is unit-tested per scheme in uriexport_test.go).

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/neurorift/neurorift/internal/yamlx"
)

// ShareInfo is the UI-facing result of exporting one node for sharing
// (issue #102).
type ShareInfo struct {
	// Name is the node's display name.
	Name string `json:"name"`
	// Type is the mihomo proxy type ("vmess", "wireguard", ...).
	Type string `json:"type"`
	// Kind is what Text holds: "uri" (a share link) or "wg-quick" (a
	// WireGuard .conf document).
	Kind string `json:"kind"`
	// Text is the share link or the wg-quick conf body.
	Text string `json:"text"`
	// QR is the QR-encoded Text as a data: URL ("data:image/png;base64,..."
	// for direct <img src> binding). Empty when Text exceeds QR capacity
	// or the type has no link form.
	QR string `json:"qr"`
}

// Share kind constants.
const (
	ShareKindURI     = "uri"
	ShareKindWGQuick = "wg-quick"
)

// ExportShareInfo serializes one stored node (map form: provider file or
// clash document) into its share format. WireGuard nodes return a
// wg-quick .conf; everything else returns a share link.
func ExportShareInfo(raw map[string]any) (*ShareInfo, error) {
	if raw == nil {
		return nil, fmt.Errorf("no node")
	}
	name := strOf(raw["name"])
	typ := strOf(raw["type"])
	if typ == "" {
		return nil, fmt.Errorf("node has no type")
	}
	var text, kind string
	switch typ {
	case "vmess":
		text, kind = exportVMessLink(raw, name), ShareKindURI
	case "vless":
		text, kind = exportVLESSLink(raw, name), ShareKindURI
	case "trojan":
		text, kind = exportTrojanLink(raw, name), ShareKindURI
	case "ss":
		text, kind = exportSSLink(raw, name), ShareKindURI
	case "ssr":
		text, kind = exportSSRLink(raw, name), ShareKindURI
	case "hysteria2":
		text, kind = exportHysteria2Link(raw, name), ShareKindURI
	case "tuic":
		text, kind = exportTUICLink(raw, name), ShareKindURI
	case "anytls":
		text, kind = exportAnyTLSLink(raw, name), ShareKindURI
	case "socks5":
		text, kind = exportSocksLink(raw, name), ShareKindURI
	case "http":
		text, kind = exportHTTPLink(raw, name), ShareKindURI
	case "wireguard":
		text, kind = exportWGQuickConf(raw), ShareKindWGQuick
	default:
		return nil, fmt.Errorf("proxy type %q has no share-link format", typ)
	}
	return &ShareInfo{
		Name: name,
		Type: typ,
		Kind: kind,
		Text: text,
		QR:   QRDataURL(text),
	}, nil
}

// ---------------------------------------------------------------------------
// Shared field readers (accept both OMap-derived and plain maps)
// ---------------------------------------------------------------------------

// normMap converts OMap (in-memory nodes) or map[string]any (nodes read
// back from provider/clash files) into a plain map.
func normMap(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case *yamlx.OMap:
		out := make(map[string]any, t.Len())
		for _, k := range t.Keys() {
			val, _ := t.Get(k)
			out[k] = val
		}
		return out
	}
	return nil
}

func strOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func intOf(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		return n
	}
	return 0
}

func boolOf(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return truthy(t)
	case int:
		return t != 0
	}
	return false
}

func strListOf(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s := strOf(e); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		return splitComma(t)
	}
	return nil
}

// nodeField reads a field of the node map.
func nodeField(raw map[string]any, key string) any { return raw[key] }

// subMap reads a nested options map ("ws-opts", "reality-opts", ...).
func subMap(raw map[string]any, key string) map[string]any {
	return normMap(raw[key])
}

// hostPort renders host:port with bracketed IPv6 literals.
func hostPort(host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]:" + strconv.Itoa(port)
	}
	return host + ":" + strconv.Itoa(port)
}

// firstStrList returns the first entry of a field that may be a string or
// a string list (mihomo's http-opts headers use both spellings in the
// wild).
func firstStrList(raw map[string]any, key string) string {
	lst := strListOf(raw[key])
	if len(lst) > 0 {
		return lst[0]
	}
	return strOf(raw[key])
}

// transportOf extracts the v2ray transport parameters (net, path, host)
// from the mihomo network fields. grpc returns its service name as path
// (the vmess JSON convention); serviceName is a separate query parameter
// for vless links.
func transportOf(raw map[string]any) (net, path, host string) {
	net = strOf(nodeField(raw, "network"))
	if net == "" {
		net = "tcp"
	}
	switch net {
	case "ws":
		o := subMap(raw, "ws-opts")
		path = strOf(o["path"])
		h := normMap(o["headers"])
		host = strOf(h["Host"])
	case "grpc":
		o := subMap(raw, "grpc-opts")
		path = strOf(o["grpc-service-name"])
	case "h2":
		o := subMap(raw, "h2-opts")
		path = strOf(o["path"])
		if l := strListOf(o["host"]); len(l) > 0 {
			host = l[0]
		}
	case "httpupgrade":
		o := subMap(raw, "httpupgrade-opts")
		path = strOf(o["path"])
		host = strOf(o["host"])
	case "splithttp":
		o := subMap(raw, "splithttp-opts")
		path = strOf(o["path"])
		host = strOf(o["host"])
	case "http":
		o := subMap(raw, "http-opts")
		path = firstStrList(o, "path")
		h := normMap(o["headers"])
		host = firstStrList(h, "Host")
	case "tcp":
		net = "tcp"
	}
	if net == "tcp" {
		net = ""
	}
	return net, path, host
}

// securityOf classifies TLS/REALITY from the node map.
// Returns (security, pbk, sid, tlsEnabled).
func securityOf(raw map[string]any) (string, string, string, bool) {
	ro := subMap(raw, "reality-opts")
	if ro != nil && ro["public-key"] != nil {
		return "reality", strOf(ro["public-key"]), strOf(ro["short-id"]), true
	}
	if boolOf(nodeField(raw, "tls")) {
		return "tls", "", "", true
	}
	return "none", "", "", false
}

// ---------------------------------------------------------------------------
// vmess:// (base64 JSON, v2rayN format)
// ---------------------------------------------------------------------------

func exportVMessLink(raw map[string]any, name string) string {
	net, path, host := transportOf(raw)
	tlsEnabled := boolOf(nodeField(raw, "tls"))
	link := vmessLink{
		V:    "2",
		PS:   name,
		Add:  strOf(nodeField(raw, "server")),
		Port: intOf(nodeField(raw, "port")),
		ID:   strOf(nodeField(raw, "uuid")),
		AID:  intOf(nodeField(raw, "alterId")),
		SCY:  orDefault(strOf(nodeField(raw, "cipher")), "auto"),
		Net:  net,
		Type: "none",
		Host: host,
		Path: path,
		SNI:  strOf(nodeField(raw, "servername")),
		FP:   strOf(nodeField(raw, "client-fingerprint")),
	}
	if tlsEnabled {
		link.TLS = "tls"
	}
	if alpn := strListOf(nodeField(raw, "alpn")); len(alpn) > 0 {
		link.ALPN = strings.Join(alpn, ",")
	}
	// Empty net means plain TCP in the vmess JSON.
	if link.Net == "" {
		link.Net = "tcp"
	}
	// mihomo's "http" network is TCP + HTTP obfs (net=tcp, type=http).
	if link.Net == "http" {
		link.Net = "tcp"
		link.Type = "http"
	}
	b, err := json.Marshal(link)
	if err != nil {
		return ""
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// vless:// (Xray URL format, incl. REALITY and post-quantum encryption)
// ---------------------------------------------------------------------------

func exportVLESSLink(raw map[string]any, name string) string {
	q := url.Values{}
	q.Set("encryption", "none")
	if enc := strOf(nodeField(raw, "encryption")); enc != "" && enc != "none" {
		q.Set("encryption", enc) // post-quantum VLESS (#90)
	}
	security, pbk, sid, _ := securityOf(raw)
	if security == "reality" {
		q.Set("security", "reality")
		if pbk != "" {
			q.Set("pbk", pbk)
		}
		if sid != "" {
			q.Set("sid", sid)
		}
	} else if security == "tls" {
		q.Set("security", "tls")
	}
	if flow := strOf(nodeField(raw, "flow")); flow != "" {
		q.Set("flow", flow)
	}
	if pe := strOf(nodeField(raw, "packet-encoding")); pe != "" {
		q.Set("packetEncoding", pe)
	}
	if sni := strOf(nodeField(raw, "servername")); sni != "" {
		q.Set("sni", sni)
	}
	if alpn := strListOf(nodeField(raw, "alpn")); len(alpn) > 0 {
		q.Set("alpn", strings.Join(alpn, ","))
	}
	if fp := strOf(nodeField(raw, "client-fingerprint")); fp != "" {
		q.Set("fp", fp)
	}
	if boolOf(nodeField(raw, "skip-cert-verify")) {
		q.Set("allowInsecure", "1")
	}
	net, path, host := transportOf(raw)
	switch net {
	case "", "tcp":
		// TCP + HTTP obfs (mihomo network "http") re-encodes as the
		// v2rayN net=tcp + headerType=http convention.
		if strOf(nodeField(raw, "network")) == "http" {
			q.Set("type", "tcp")
			q.Set("headerType", "http")
			if path != "" {
				q.Set("path", path)
			}
			if host != "" {
				q.Set("host", host)
			}
		}
	case "ws":
		q.Set("type", "ws")
		if path != "" {
			q.Set("path", path)
		}
		if host != "" {
			q.Set("host", host)
		}
	case "grpc":
		q.Set("type", "grpc")
		if path != "" {
			q.Set("serviceName", path)
		}
	case "h2":
		q.Set("type", "h2")
		if path != "" {
			q.Set("path", path)
		}
		if host != "" {
			q.Set("host", host)
		}
	case "httpupgrade", "splithttp":
		q.Set("type", net)
		if path != "" {
			q.Set("path", path)
		}
		if host != "" {
			q.Set("host", host)
		}
	}
	u := url.URL{
		Scheme:   "vless",
		User:     url.User(strOf(nodeField(raw, "uuid"))),
		Host:     hostPort(strOf(nodeField(raw, "server")), intOf(nodeField(raw, "port"))),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String()
}

// ---------------------------------------------------------------------------
// trojan://
// ---------------------------------------------------------------------------

func exportTrojanLink(raw map[string]any, name string) string {
	q := url.Values{}
	security, pbk, sid, _ := securityOf(raw)
	switch security {
	case "reality":
		q.Set("security", "reality")
		if pbk != "" {
			q.Set("pbk", pbk)
		}
		if sid != "" {
			q.Set("sid", sid)
		}
	case "none":
		q.Set("security", "none")
	}
	if sni := strOf(nodeField(raw, "sni")); sni != "" {
		q.Set("sni", sni)
	}
	if alpn := strListOf(nodeField(raw, "alpn")); len(alpn) > 0 {
		q.Set("alpn", strings.Join(alpn, ","))
	}
	if fp := strOf(nodeField(raw, "client-fingerprint")); fp != "" {
		q.Set("fp", fp)
	}
	if boolOf(nodeField(raw, "skip-cert-verify")) {
		q.Set("allowInsecure", "1")
	}
	net, path, host := transportOf(raw)
	switch net {
	case "ws":
		q.Set("type", "ws")
		if path != "" {
			q.Set("path", path)
		}
		if host != "" {
			q.Set("host", host)
		}
	case "grpc":
		q.Set("type", "grpc")
		if path != "" {
			q.Set("serviceName", path)
		}
	}
	u := url.URL{
		Scheme:   "trojan",
		User:     url.User(strOf(nodeField(raw, "password"))),
		Host:     hostPort(strOf(nodeField(raw, "server")), intOf(nodeField(raw, "port"))),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String()
}

// ---------------------------------------------------------------------------
// ss://  (SIP002, incl. obfs / v2ray plugins)
// ---------------------------------------------------------------------------

func exportSSLink(raw map[string]any, name string) string {
	userInfo := base64.RawURLEncoding.EncodeToString(
		[]byte(strOf(nodeField(raw, "cipher")) + ":" + strOf(nodeField(raw, "password"))))
	var query string
	plugin := strOf(nodeField(raw, "plugin"))
	if plugin != "" {
		po := subMap(raw, "plugin-opts")
		var p string
		switch plugin {
		case "obfs":
			p = "obfs-local;obfs=" + orDefault(strOf(po["mode"]), "http")
			if h := strOf(po["host"]); h != "" {
				p += ";obfs-host=" + h
			}
		case "v2ray-plugin":
			p = "v2ray-plugin;mode=" + orDefault(strOf(po["mode"]), "websocket")
			if boolOf(po["tls"]) {
				p += ";tls"
			}
			if h := strOf(po["host"]); h != "" {
				p += ";host=" + h
			}
			if pt := strOf(po["path"]); pt != "" {
				p += ";path=" + pt
			}
		}
		if p != "" {
			query = "?" + url.Values{"plugin": []string{p}}.Encode()
		}
	}
	return "ss://" + userInfo + "@" +
		hostPort(strOf(nodeField(raw, "server")), intOf(nodeField(raw, "port"))) +
		query + "#" + name
}

// ---------------------------------------------------------------------------
// ssr://  (legacy base64 format)
// ---------------------------------------------------------------------------

func exportSSRLink(raw map[string]any, name string) string {
	b64u := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	main := strings.Join([]string{
		strOf(nodeField(raw, "server")),
		strconv.Itoa(intOf(nodeField(raw, "port"))),
		strOf(nodeField(raw, "protocol")),
		strOf(nodeField(raw, "cipher")),
		strOf(nodeField(raw, "obfs")),
		b64u(strOf(nodeField(raw, "password"))),
	}, ":")
	params := url.Values{}
	params.Set("remarks", b64u(name))
	if pp := strOf(nodeField(raw, "protocol-param")); pp != "" {
		params.Set("protoparam", b64u(pp))
	}
	if op := strOf(nodeField(raw, "obfs-param")); op != "" {
		params.Set("obfsparam", b64u(op))
	}
	return "ssr://" + b64u(main+"/?"+params.Encode())
}

// ---------------------------------------------------------------------------
// hysteria2:// / tuic:// / anytls://
// ---------------------------------------------------------------------------

func exportHysteria2Link(raw map[string]any, name string) string {
	q := url.Values{}
	if ports := strOf(nodeField(raw, "ports")); ports != "" {
		q.Set("mport", ports)
	}
	if obfs := strOf(nodeField(raw, "obfs")); obfs != "" {
		q.Set("obfs", obfs)
		if opw := strOf(nodeField(raw, "obfs-password")); opw != "" {
			q.Set("obfs-password", opw)
		}
	}
	if sni := strOf(nodeField(raw, "sni")); sni != "" {
		q.Set("sni", sni)
	}
	if boolOf(nodeField(raw, "skip-cert-verify")) {
		q.Set("insecure", "1")
	}
	if pin := strOf(nodeField(raw, "fingerprint")); pin != "" {
		q.Set("pinSHA256", pin)
	}
	u := url.URL{
		Scheme:   "hysteria2",
		User:     url.User(strOf(nodeField(raw, "password"))),
		Host:     hostPort(strOf(nodeField(raw, "server")), intOf(nodeField(raw, "port"))),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String()
}

func exportTUICLink(raw map[string]any, name string) string {
	q := url.Values{}
	if cc := strOf(nodeField(raw, "congestion-controller")); cc != "" {
		q.Set("congestion_control", cc)
	}
	if ur := strOf(nodeField(raw, "udp-relay-mode")); ur != "" {
		q.Set("udp_relay_mode", ur)
	}
	if sni := strOf(nodeField(raw, "sni")); sni != "" {
		q.Set("sni", sni)
	}
	if alpn := strListOf(nodeField(raw, "alpn")); len(alpn) > 0 {
		q.Set("alpn", strings.Join(alpn, ","))
	}
	if boolOf(nodeField(raw, "skip-cert-verify")) {
		q.Set("allow_insecure", "1")
	}
	u := url.URL{
		Scheme:   "tuic",
		Host:     hostPort(strOf(nodeField(raw, "server")), intOf(nodeField(raw, "port"))),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	if pass := strOf(nodeField(raw, "password")); pass != "" {
		u.User = url.UserPassword(strOf(nodeField(raw, "uuid")), pass)
	} else {
		u.User = url.User(strOf(nodeField(raw, "token")))
	}
	return u.String()
}

func exportAnyTLSLink(raw map[string]any, name string) string {
	q := url.Values{}
	if sni := strOf(nodeField(raw, "sni")); sni != "" {
		q.Set("sni", sni)
	}
	if boolOf(nodeField(raw, "skip-cert-verify")) {
		q.Set("insecure", "1")
	}
	if fp := strOf(nodeField(raw, "client-fingerprint")); fp != "" {
		q.Set("fp", fp)
	}
	if alpn := strListOf(nodeField(raw, "alpn")); len(alpn) > 0 {
		q.Set("alpn", strings.Join(alpn, ","))
	}
	u := url.URL{
		Scheme:   "anytls",
		User:     url.User(strOf(nodeField(raw, "password"))),
		Host:     hostPort(strOf(nodeField(raw, "server")), intOf(nodeField(raw, "port"))),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String()
}

// ---------------------------------------------------------------------------
// socks5:// and http(s)://
// ---------------------------------------------------------------------------

func exportSocksLink(raw map[string]any, name string) string {
	scheme := "socks5"
	if boolOf(nodeField(raw, "tls")) {
		scheme = "socks5s"
	}
	u := url.URL{
		Scheme:   scheme,
		Host:     hostPort(strOf(nodeField(raw, "server")), intOf(nodeField(raw, "port"))),
		Fragment: name,
	}
	if user := strOf(nodeField(raw, "username")); user != "" {
		u.User = url.UserPassword(user, strOf(nodeField(raw, "password")))
	}
	return u.String()
}

func exportHTTPLink(raw map[string]any, name string) string {
	scheme := "http"
	if boolOf(nodeField(raw, "tls")) {
		scheme = "https"
	}
	u := url.URL{
		Scheme:   scheme,
		Host:     hostPort(strOf(nodeField(raw, "server")), intOf(nodeField(raw, "port"))),
		Fragment: name,
	}
	if user := strOf(nodeField(raw, "username")); user != "" {
		u.User = url.UserPassword(user, strOf(nodeField(raw, "password")))
	}
	return u.String()
}

// ---------------------------------------------------------------------------
// wireguard → wg-quick .conf (inverse of wgquick.go)
// ---------------------------------------------------------------------------

// exportWGQuickConf renders a mihomo wireguard node back into a standard
// wg-quick configuration file. Simplified-syntax nodes (single peer) and
// full peers-list nodes both emit one [Peer] block per peer.
func exportWGQuickConf(raw map[string]any) string {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	var addrs []string
	for _, k := range []string{"ip", "ipv6"} {
		if v := strOf(nodeField(raw, k)); v != "" {
			addrs = append(addrs, v)
		}
	}
	if len(addrs) > 0 {
		b.WriteString("Address = " + strings.Join(addrs, ", ") + "\n")
	}
	b.WriteString("PrivateKey = " + strOf(nodeField(raw, "private-key")) + "\n")
	if dns := strListOf(nodeField(raw, "dns")); len(dns) > 0 {
		b.WriteString("DNS = " + strings.Join(dns, ", ") + "\n")
	}
	if mtu := intOf(nodeField(raw, "mtu")); mtu > 0 {
		b.WriteString("MTU = " + strconv.Itoa(mtu) + "\n")
	}
	// AmneziaWG options use wg-quick's original capitalized key names.
	if awg := subMap(raw, "amnezia-wg-option"); len(awg) > 0 {
		for _, k := range []string{"Jc", "Jmin", "Jmax", "S1", "S2", "S3", "S4", "H1", "H2", "H3", "H4", "I1", "I2", "I3", "I4", "I5"} {
			low := strings.ToLower(k)
			if v, ok := awg[low]; ok && v != nil {
				b.WriteString(k + " = " + fmt.Sprint(v) + "\n")
			}
		}
	}

	type wgPeer struct {
		server string
		port   int
		pub    string
		psk    string
		ips    []string
		keep   int
	}
	var peers []wgPeer
	switch list := nodeField(raw, "peers").(type) {
	case []any: // provider file (yaml) form
		for _, item := range list {
			if pm := normMap(item); pm != nil {
				peers = append(peers, wgPeer{
					server: strOf(pm["server"]),
					port:   intOf(pm["port"]),
					pub:    strOf(pm["public-key"]),
					psk:    strOf(pm["pre-shared-key"]),
					ips:    strListOf(pm["allowed-ips"]),
				})
			}
		}
	case []*yamlx.OMap: // in-memory node form
		for _, pm := range list {
			m := normMap(pm)
			peers = append(peers, wgPeer{
				server: strOf(m["server"]),
				port:   intOf(m["port"]),
				pub:    strOf(m["public-key"]),
				psk:    strOf(m["pre-shared-key"]),
				ips:    strListOf(m["allowed-ips"]),
			})
		}
	}
	if len(peers) == 0 {
		p := wgPeer{
			server: strOf(nodeField(raw, "server")),
			port:   intOf(nodeField(raw, "port")),
			pub:    strOf(nodeField(raw, "public-key")),
			psk:    strOf(nodeField(raw, "pre-shared-key")),
			ips:    strListOf(nodeField(raw, "allowed-ips")),
			keep:   intOf(nodeField(raw, "persistent-keepalive")),
		}
		peers = append(peers, p)
	}
	for _, p := range peers {
		b.WriteString("\n[Peer]\n")
		if p.pub != "" {
			b.WriteString("PublicKey = " + p.pub + "\n")
		}
		if p.server != "" {
			b.WriteString("Endpoint = " + hostPort(p.server, p.port) + "\n")
		}
		if len(p.ips) > 0 {
			b.WriteString("AllowedIPs = " + strings.Join(p.ips, ", ") + "\n")
		}
		if p.psk != "" {
			b.WriteString("PresharedKey = " + p.psk + "\n")
		}
		if p.keep > 0 {
			b.WriteString("PersistentKeepalive = " + strconv.Itoa(p.keep) + "\n")
		}
	}
	return b.String()
}
