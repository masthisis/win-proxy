package profile

// singbox.go imports sing-box JSON configurations into mihomo nodes.
//
// Scope (issue #16): NODES ONLY. sing-box proxy-groups ("selector"/"urltest")
// and route rules are deliberately NOT converted — mihomo rule semantics and
// NeuroRift's own routing overrides differ; users rebuild groups in the app.
// Outbounds that cannot be mapped are skipped and COUNTED; the count is
// surfaced through the normal import progress detail (see manager.go
// doneDetail), following the conservative import discipline of sanitize.go:
// never import a half-broken provider.
//
// Field mapping follows the sing-box outbound schema (sing-box.sagernet.org)
// and mihomo's proxy schema (wiki.metacubex.one). The integration test boots
// the real core with generated configs and is the schema authority; these
// unit tests pin the JSON->YAML mapping itself.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/neurorift/neurorift/internal/yamlx"
)

// looksLikeSingboxJSON reports whether text parses as a JSON object carrying
// a non-empty "outbounds" array (the sing-box config fingerprint).
func looksLikeSingboxJSON(text string) bool {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "{") {
		return false
	}
	var probe struct {
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(t), &probe); err != nil {
		return false
	}
	return len(probe.Outbounds) > 0
}

type singboxConfig struct {
	Outbounds []singboxOutbound `json:"outbounds"`
}

// singboxOutbound is the union of fields from all convertible outbound
// types. sing-box ignores unknown keys, so one flat struct is safe.
type singboxOutbound struct {
	Tag  string `json:"tag"`
	Type string `json:"type"`

	Server     string `json:"server"`
	ServerPort any    `json:"server_port"`

	// shadowsocks
	Method     string          `json:"method"`
	Password   string          `json:"password"`
	Plugin     string          `json:"plugin"`
	PluginOpts json.RawMessage `json:"plugin_opts"`
	UDPOverTCP bool            `json:"udp_over_tcp"`

	// vmess / vless / tuic
	UUID           string `json:"uuid"`
	Security       string `json:"security"`
	AlterID        any    `json:"alter_id"`
	Flow           string `json:"flow"`
	PacketEncoding string `json:"packet_encoding"`

	// tls + transport + multiplex (shared blocks)
	TLS       *singboxTLS       `json:"tls"`
	Transport *singboxTransport `json:"transport"`
	Multiplex *singboxMux       `json:"multiplex"`

	// hysteria2
	UpMbps      any          `json:"up_mbps"`
	DownMbps    any          `json:"down_mbps"`
	Obfs        *singboxObfs `json:"obfs"`
	ServerPorts []string     `json:"server_ports"`

	// tuic
	CongestionControl string `json:"congestion_control"`
	UDPRelayMode      string `json:"udp_relay_mode"`

	// socks / http
	Version  string `json:"version"`
	Username string `json:"username"`

	// wireguard
	LocalAddress  []string `json:"local_address"`
	PrivateKey    string   `json:"private_key"`
	PeerPublicKey string   `json:"peer_public_key"`
	PreSharedKey  string   `json:"pre_shared_key"`
	Reserved      any      `json:"reserved"`
	MTU           any      `json:"mtu"`

	// chain through another outbound (mapped to mihomo dialer-proxy)
	Detour string `json:"detour"`
}

type singboxTLS struct {
	Enabled    bool     `json:"enabled"`
	ServerName string   `json:"server_name"`
	ALPN       []string `json:"alpn"`
	Insecure   bool     `json:"insecure"`
	UTLS       *struct {
		Enabled     bool   `json:"enabled"`
		Fingerprint string `json:"fingerprint"`
	} `json:"utls"`
	Reality *struct {
		Enabled   bool   `json:"enabled"`
		PublicKey string `json:"public_key"`
		ShortID   string `json:"short_id"`
	} `json:"reality"`
}

type singboxTransport struct {
	Type string `json:"type"`
	Path string `json:"path"`
	// Headers: ws carries arbitrary headers (Host usually); http2 uses Host.
	Headers     map[string]string `json:"headers"`
	ServiceName string            `json:"service_name"`
	// ws early data (mapped to mihomo ws-opts early-data fields)
	MaxEarlyData        any    `json:"max_early_data"`
	EarlyDataHeaderName string `json:"early_data_header_name"`
}

type singboxMux struct {
	Enabled        bool   `json:"enabled"`
	Protocol       string `json:"protocol"`
	MaxConnections any    `json:"max_connections"`
	MaxStreams     any    `json:"max_streams"`
	MinStreams     any    `json:"min_streams"`
	Padding        bool   `json:"padding"`
}

type singboxObfs struct {
	Type     string `json:"type"`
	Password string `json:"password"`
}

// decodeSingbox converts sing-box JSON into mihomo nodes (KindNodes).
func decodeSingbox(text string) (*DecodeResult, error) {
	var cfg singboxConfig
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &cfg); err != nil {
		return nil, fmt.Errorf("sing-box json: %w", err)
	}
	if len(cfg.Outbounds) == 0 {
		return nil, errors.New("sing-box json contains no outbounds")
	}

	var nodes []Node
	var warns []string
	type link struct {
		node   *Node
		detour string
	}
	converted := map[string]string{} // sing-box tag -> imported node name
	var chained []link

	for i := range cfg.Outbounds {
		ob := &cfg.Outbounds[i]
		tag := ob.Tag
		if tag == "" {
			tag = fmt.Sprintf("outbound-%d", i)
		}
		if _, dup := converted[tag]; dup {
			warns = append(warns, fmt.Sprintf("skipped outbound %q: duplicate tag", ob.Tag))
			continue
		}
		m, err := convertSingboxOutbound(ob, tag)
		if err != nil {
			warns = append(warns, fmt.Sprintf("skipped outbound %q (%s): %v", ob.Tag, ob.Type, err))
			continue
		}
		name, _ := m.Get("name")
		converted[tag] = str(name)
		nodes = append(nodes, Node{Name: str(name), Raw: m})
		if ob.Detour != "" {
			chained = append(chained, link{node: &nodes[len(nodes)-1], detour: ob.Detour})
		}
	}

	// Resolve detour chains: mihomo's dialer-proxy has the same semantics as
	// sing-box's detour. A chain whose upstream was itself skipped (e.g.
	// shadow-tls) cannot work: drop the downstream node with a warning
	// instead of writing a provider the core would reject wholesale.
	for _, c := range chained {
		upstream, ok := converted[c.detour]
		if !ok {
			// Remove the node we just added.
			name := c.node.Name
			for i := range nodes {
				if nodes[i].Name == name {
					nodes = append(nodes[:i], nodes[i+1:]...)
					break
				}
			}
			delete(converted, name)
			warns = append(warns, fmt.Sprintf("skipped outbound %q: detour %q was not imported", name, c.detour))
			continue
		}
		c.node.Raw.(*yamlx.OMap).Set("dialer-proxy", upstream)
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("no usable sing-box outbounds (%d skipped)", len(warns))
	}
	var sanitizeWarns []string
	nodes, sanitizeWarns = sanitizeNodes(nodes)
	warns = append(warns, sanitizeWarns...)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("all sing-box outbounds failed validation (%d dropped)", len(sanitizeWarns))
	}
	dedupeNames(&nodes)
	return &DecodeResult{Parsed: &ParsedContent{Kind: KindNodes, Nodes: nodes}, Warnings: warns}, nil
}

// convertSingboxOutbound maps one outbound to a mihomo proxy OMap.
func convertSingboxOutbound(ob *singboxOutbound, tag string) (*yamlx.OMap, error) {
	switch ob.Type {
	case "shadowsocks":
		return singboxSS(ob, tag)
	case "vmess":
		return singboxVMess(ob, tag)
	case "vless":
		return singboxVLESS(ob, tag)
	case "trojan":
		return singboxTrojan(ob, tag)
	case "hysteria2":
		return singboxHysteria2(ob, tag)
	case "tuic":
		return singboxTUIC(ob, tag)
	case "wireguard":
		return singboxWireguard(ob, tag)
	case "socks":
		return singboxSocks(ob, tag)
	case "http":
		return singboxHTTP(ob, tag)
	default:
		// selector/urltest are groups; direct/block/dns are plumbing.
		return nil, fmt.Errorf("unsupported outbound type %q", ob.Type)
	}
}

// singboxTLSBlock applies the shared tls block. sniKey is "servername" for
// vmess/vless and "sni" for trojan/hysteria2/tuic.
func singboxTLSBlock(m *yamlx.OMap, tls *singboxTLS, sniKey string) {
	if tls == nil || !tls.Enabled {
		return
	}
	fp := ""
	insecure := tls.Insecure
	if tls.UTLS != nil && tls.UTLS.Enabled {
		fp = tls.UTLS.Fingerprint
	}
	if tls.Reality != nil && tls.Reality.Enabled {
		ro := newOMap()
		if tls.Reality.PublicKey != "" {
			ro.Set("public-key", tls.Reality.PublicKey)
		}
		if tls.Reality.ShortID != "" {
			ro.Set("short-id", tls.Reality.ShortID)
		}
		if ro.Len() > 0 {
			m.Set("reality-opts", ro)
		}
		if fp == "" {
			fp = "chrome" // REALITY requires a uTLS fingerprint (same as links)
		}
	}
	applyTLS(m, true, sniKey, tls.ServerName, tls.ALPN, insecure, fp)
}

// singboxTransportBlock applies the shared transport block. Returns an error
// for transports mihomo cannot express.
func singboxTransportBlock(m *yamlx.OMap, tr *singboxTransport) error {
	if tr == nil {
		return nil
	}
	switch tr.Type {
	case "", "tcp":
		return nil
	case "ws", "websocket":
		opts := newOMap().Set("path", orDefault(tr.Path, "/"))
		if len(tr.Headers) > 0 {
			h := newOMap()
			for k, v := range tr.Headers {
				h.Set(k, v)
			}
			opts.Set("headers", h)
		}
		if ed := anyToInt(tr.MaxEarlyData, 0); ed > 0 {
			opts.Set("max-early-data", ed)
			if tr.EarlyDataHeaderName != "" {
				opts.Set("early-data-header-name", tr.EarlyDataHeaderName)
			}
		}
		m.Set("network", "ws").Set("ws-opts", opts)
	case "grpc":
		opts := newOMap().Set("grpc-service-name", orDefault(tr.ServiceName, tr.Path))
		m.Set("network", "grpc").Set("grpc-opts", opts)
	case "http2", "h2", "http":
		// sing-box's "http" transport IS the h2 transport (v2ray net=h2).
		opts := newOMap().Set("path", orDefault(tr.Path, "/"))
		if h := tr.Headers["Host"]; h != "" {
			opts.Set("host", []string{h})
		}
		m.Set("network", "h2").Set("h2-opts", opts)
	case "httpupgrade":
		opts := newOMap().Set("path", orDefault(tr.Path, "/"))
		if h := tr.Headers["Host"]; h != "" {
			opts.Set("host", h)
		}
		m.Set("network", "httpupgrade").Set("httpupgrade-opts", opts)
	default:
		return fmt.Errorf("unsupported transport %q", tr.Type)
	}
	return nil
}

// singboxMuxBlock maps sing-box multiplex onto mihomo smux.
func singboxMuxBlock(m *yamlx.OMap, mux *singboxMux) {
	if mux == nil || !mux.Enabled {
		return
	}
	s := newOMap().Set("enabled", true)
	if mux.Protocol != "" {
		s.Set("protocol", mux.Protocol)
	}
	if v := anyToInt(mux.MaxConnections, 0); v > 0 {
		s.Set("max-connections", v)
	}
	if v := anyToInt(mux.MaxStreams, 0); v > 0 {
		s.Set("max-streams", v)
	}
	if v := anyToInt(mux.MinStreams, 0); v > 0 {
		s.Set("min-streams", v)
	}
	if mux.Padding {
		s.Set("padding", true)
	}
	m.Set("smux", s)
}

// singboxPacketEncoding carries over for vmess/vless.
func singboxPacketEncoding(m *yamlx.OMap, pe string) {
	if pe != "" {
		m.Set("packet-encoding", pe)
	}
}

// --- shadowsocks ------------------------------------------------------------

func singboxSS(ob *singboxOutbound, tag string) (*yamlx.OMap, error) {
	host, port, err := parseHostPort(ob.Server, anyToStr(ob.ServerPort), 0)
	if err != nil {
		return nil, err
	}
	if ob.Method == "" {
		return nil, errors.New("missing method")
	}
	m := newOMap().
		Set("name", tag).
		Set("type", "ss").
		Set("server", host).
		Set("port", port).
		Set("cipher", ob.Method).
		Set("password", ob.Password)
	if ob.UDPOverTCP {
		m.Set("udp-over-tcp", true)
	}
	if ob.Plugin != "" {
		switch ob.Plugin {
		case "obfs":
			var opts struct {
				Mode string `json:"mode"`
				Host string `json:"host"`
			}
			_ = json.Unmarshal(ob.PluginOpts, &opts)
			po := newOMap().Set("mode", orDefault(opts.Mode, "http"))
			if opts.Host != "" {
				po.Set("host", opts.Host)
			}
			m.Set("plugin", "obfs").Set("plugin-opts", po)
		case "v2ray-plugin":
			var opts struct {
				Mode string `json:"mode"`
				Host string `json:"host"`
				Path string `json:"path"`
				TLS  bool   `json:"tls"`
			}
			_ = json.Unmarshal(ob.PluginOpts, &opts)
			po := newOMap().Set("mode", orDefault(opts.Mode, "websocket"))
			if opts.Host != "" {
				po.Set("host", opts.Host)
			}
			if opts.Path != "" {
				po.Set("path", opts.Path)
			}
			if opts.TLS {
				po.Set("tls", true)
			}
			m.Set("plugin", "v2ray-plugin").Set("plugin-opts", po)
		default:
			return nil, fmt.Errorf("unsupported plugin %q", ob.Plugin)
		}
	}
	return m, nil
}

// --- vmess -------------------------------------------------------------------

func singboxVMess(ob *singboxOutbound, tag string) (*yamlx.OMap, error) {
	host, port, err := singboxHostPort(ob)
	if err != nil {
		return nil, err
	}
	if ob.UUID == "" {
		return nil, errors.New("missing uuid")
	}
	m := newOMap().
		Set("name", tag).
		Set("type", "vmess").
		Set("server", host).
		Set("port", port).
		Set("uuid", ob.UUID).
		Set("alterId", anyToInt(ob.AlterID, 0)).
		Set("cipher", orDefault(ob.Security, "auto")).
		Set("udp", true)
	if err := singboxTransportBlock(m, ob.Transport); err != nil {
		return nil, err
	}
	singboxTLSBlock(m, ob.TLS, "servername")
	singboxPacketEncoding(m, ob.PacketEncoding)
	singboxMuxBlock(m, ob.Multiplex)
	return m, nil
}

// --- vless -------------------------------------------------------------------

func singboxVLESS(ob *singboxOutbound, tag string) (*yamlx.OMap, error) {
	host, port, err := singboxHostPort(ob)
	if err != nil {
		return nil, err
	}
	if ob.UUID == "" {
		return nil, errors.New("missing uuid")
	}
	m := newOMap().
		Set("name", tag).
		Set("type", "vless").
		Set("server", host).
		Set("port", port).
		Set("uuid", ob.UUID).
		Set("udp", true)
	if ob.Flow != "" {
		m.Set("flow", ob.Flow)
	}
	if err := singboxTransportBlock(m, ob.Transport); err != nil {
		return nil, err
	}
	singboxTLSBlock(m, ob.TLS, "servername")
	singboxPacketEncoding(m, ob.PacketEncoding)
	singboxMuxBlock(m, ob.Multiplex)
	return m, nil
}

// --- trojan ------------------------------------------------------------------

func singboxTrojan(ob *singboxOutbound, tag string) (*yamlx.OMap, error) {
	host, port, err := singboxHostPort(ob)
	if err != nil {
		return nil, err
	}
	if ob.Password == "" {
		return nil, errors.New("missing password")
	}
	m := newOMap().
		Set("name", tag).
		Set("type", "trojan").
		Set("server", host).
		Set("port", port).
		Set("password", ob.Password).
		Set("udp", true)
	if err := singboxTransportBlock(m, ob.Transport); err != nil {
		return nil, err
	}
	singboxTLSBlock(m, ob.TLS, "sni")
	singboxMuxBlock(m, ob.Multiplex)
	return m, nil
}

// --- hysteria2 ---------------------------------------------------------------

func singboxHysteria2(ob *singboxOutbound, tag string) (*yamlx.OMap, error) {
	host, port, err := singboxHostPort(ob)
	if err != nil {
		return nil, err
	}
	m := newOMap().
		Set("name", tag).
		Set("type", "hysteria2").
		Set("server", host).
		Set("port", port).
		Set("password", ob.Password).
		Set("udp", true)
	if up := anyToInt(ob.UpMbps, 0); up > 0 {
		m.Set("up", up)
	}
	if down := anyToInt(ob.DownMbps, 0); down > 0 {
		m.Set("down", down)
	}
	// Port hopping: sing-box server_ports ["443:3000"] -> mihomo ports
	// "443-3000" (comma-joined).
	if len(ob.ServerPorts) > 0 {
		ranges := make([]string, 0, len(ob.ServerPorts))
		for _, r := range ob.ServerPorts {
			ranges = append(ranges, strings.ReplaceAll(strings.TrimSpace(r), ":", "-"))
		}
		if s := strings.Join(ranges, ","); s != "" {
			m.Set("ports", s)
		}
	}
	if ob.Obfs != nil && ob.Obfs.Type != "" {
		m.Set("obfs", ob.Obfs.Type)
		if ob.Obfs.Password != "" {
			m.Set("obfs-password", ob.Obfs.Password)
		}
	}
	singboxTLSBlock(m, ob.TLS, "sni")
	return m, nil
}

// --- tuic --------------------------------------------------------------------

func singboxTUIC(ob *singboxOutbound, tag string) (*yamlx.OMap, error) {
	host, port, err := singboxHostPort(ob)
	if err != nil {
		return nil, err
	}
	if ob.UUID == "" {
		return nil, errors.New("missing uuid")
	}
	m := newOMap().
		Set("name", tag).
		Set("type", "tuic").
		Set("server", host).
		Set("port", port).
		Set("uuid", ob.UUID).
		Set("udp", true)
	if ob.Password != "" {
		m.Set("password", ob.Password)
	}
	if ob.CongestionControl != "" {
		m.Set("congestion-controller", ob.CongestionControl)
	}
	if ob.UDPRelayMode != "" {
		m.Set("udp-relay-mode", ob.UDPRelayMode)
	}
	singboxTLSBlock(m, ob.TLS, "sni")
	return m, nil
}

// --- wireguard ---------------------------------------------------------------

func singboxWireguard(ob *singboxOutbound, tag string) (*yamlx.OMap, error) {
	host, port, err := singboxHostPort(ob)
	if err != nil {
		return nil, err
	}
	if ob.PrivateKey == "" {
		return nil, errors.New("missing private_key")
	}
	m := newOMap().
		Set("name", tag).
		Set("type", "wireguard").
		Set("server", host).
		Set("port", port).
		Set("private-key", ob.PrivateKey).
		Set("public-key", ob.PeerPublicKey).
		Set("udp", true)
	// local_address entries split into ip / ipv6.
	for _, addr := range ob.LocalAddress {
		if strings.Contains(addr, ":") {
			if _, ok := m.Get("ipv6"); !ok {
				m.Set("ipv6", addr)
			}
		} else if _, ok := m.Get("ip"); !ok {
			m.Set("ip", addr)
		}
	}
	if _, ok := m.Get("ip"); !ok {
		if _, ok := m.Get("ipv6"); !ok {
			return nil, errors.New("missing local_address")
		}
	}
	if ob.PreSharedKey != "" {
		m.Set("pre-shared-key", ob.PreSharedKey)
	}
	if res := singboxReserved(ob.Reserved); len(res) > 0 {
		m.Set("reserved", res)
	}
	if mtu := anyToInt(ob.MTU, 0); mtu > 0 {
		m.Set("mtu", mtu)
	}
	return m, nil
}

// singboxReserved normalizes the reserved field: sing-box allows either a
// byte array or a base64 string; mihomo takes an int array (string is also
// legal, but the array is unambiguous).
func singboxReserved(v any) []int {
	switch t := v.(type) {
	case []any:
		out := make([]int, 0, len(t))
		for _, e := range t {
			if n := anyToInt(e, -1); n >= 0 {
				out = append(out, n)
			}
		}
		return out
	case string:
		if b, err := base64.StdEncoding.DecodeString(t); err == nil {
			return bytesToReserved(b)
		}
		if b, err := base64.RawStdEncoding.DecodeString(t); err == nil {
			return bytesToReserved(b)
		}
	}
	return nil
}

func bytesToReserved(b []byte) []int {
	out := make([]int, len(b))
	for i, c := range b {
		out[i] = int(c)
	}
	return out
}

// --- socks / http ------------------------------------------------------------

func singboxSocks(ob *singboxOutbound, tag string) (*yamlx.OMap, error) {
	host, port, err := singboxHostPort(ob)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(ob.Version) {
	case "5", "":
	default:
		return nil, fmt.Errorf("unsupported socks version %q", ob.Version)
	}
	m := newOMap().
		Set("name", tag).
		Set("type", "socks5").
		Set("server", host).
		Set("port", port).
		Set("udp", true)
	if ob.Username != "" {
		m.Set("username", ob.Username)
	}
	if ob.Password != "" {
		m.Set("password", ob.Password)
	}
	return m, nil
}

func singboxHTTP(ob *singboxOutbound, tag string) (*yamlx.OMap, error) {
	host, port, err := singboxHostPort(ob)
	if err != nil {
		return nil, err
	}
	m := newOMap().
		Set("name", tag).
		Set("type", "http").
		Set("server", host).
		Set("port", port)
	if ob.Username != "" {
		m.Set("username", ob.Username)
	}
	if ob.Password != "" {
		m.Set("password", ob.Password)
	}
	singboxTLSBlock(m, ob.TLS, "sni")
	return m, nil
}

// singboxHostPort parses the shared server/server_port pair.
func singboxHostPort(ob *singboxOutbound) (string, int, error) {
	host, port, err := parseHostPort(ob.Server, anyToStr(ob.ServerPort), 0)
	if err != nil {
		return "", 0, fmt.Errorf("%s: %w", ob.Type, err)
	}
	if err := validServer(host); err != nil {
		return "", 0, fmt.Errorf("%s: %w", ob.Type, err)
	}
	return host, port, nil
}

// anyToStr converts a JSON number (float64) or string to its string form.
func anyToStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	}
	return ""
}
