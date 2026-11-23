package profile

// wgquick.go imports standard WireGuard wg-quick configuration files
// (owner chat directive 2026-11-23) as native mihomo wireguard proxy
// nodes. A .conf becomes a real node that joins the Proxies view, speed
// testing, url-test auto-switching, rules and traffic display — no helper
// processes, everything runs inside the core.
//
// The translation follows the official mapping documented by mihomo
// (wiki.metacubex.one/en/config/proxies/wg/):
//
//    [Interface] Address  -> ip / ipv6 (first entry per family, CIDR kept)
//    [Interface] PrivateKey -> private-key
//    [Interface] DNS      -> remote-dns-resolve: true + dns: [...]
//    [Interface] MTU      -> mtu
//    [Interface] Jc/Jmin/Jmax/S1-S4/H1-H4/I1-I5 -> amnezia-wg-option
//    [Peer] Endpoint      -> server / port (bare host defaults to 51820)
//    [Peer] PublicKey     -> public-key
//    [Peer] AllowedIPs    -> allowed-ips (default ['0.0.0.0/0'])
//    [Peer] PresharedKey  -> pre-shared-key
//    [Peer] PersistentKeepalive -> persistent-keepalive (simplified syntax
//                            only: mihomo's peers list has no per-peer
//                            keepalive field)
//
// One peer emits mihomo's simplified syntax (flat node); two or more peers
// emit the full syntax (peers: list), in which form mihomo ignores
// top-level server/port/public-key and requires DISTINCT allowed-ips per
// peer. wg-quick plumbing with no mihomo equivalent (Table, FwMark,
// PreUp/PostUp, ListenPort, SaveConfig, ...) is ignored silently: mihomo
// owns routing and never touches the host network stack.
//
// Import discipline follows sanitize.go: 32-byte base64 validation on all
// keys, unconvertible peers are dropped with countable warnings, and the
// import fails only when nothing usable remains.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/neurorift/neurorift/internal/yamlx"
)

// looksLikeWGQuick reports whether text smells like a wg-quick .conf: an
// [Interface] section plus a private key or a peer. Clash YAML and sing-box
// JSON are excluded first (they are matched earlier in DecodeContent too,
// but the detector must be safe to call in any order).
func looksLikeWGQuick(text string) bool {
	if looksLikeClashYAML(text) || looksLikeSingboxJSON(text) {
		return false
	}
	lower := strings.ToLower(text)
	return strings.Contains(lower, "[interface]") &&
		(strings.Contains(lower, "privatekey") || strings.Contains(lower, "[peer]"))
}

// wgQuickConf is a parsed wg-quick file: [Interface] keys plus the ordered
// [Peer] sections. Keys are lowercased — wg-quick's INI format is
// case-insensitive (AmneziaWG writes Jc/S1/H1, standard tools write
// PrivateKey/AllowedIPs).
type wgQuickConf struct {
	iface map[string]string
	peers []map[string]string
}

// parseWGQuick parses the INI structure of a wg-quick .conf. Unknown
// sections are skipped; lines without "=" are rejected the same way
// wg-quick itself rejects them.
func parseWGQuick(text string) (*wgQuickConf, error) {
	conf := &wgQuickConf{iface: map[string]string{}}
	section := "" // "", "interface" or "peer"
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			name := strings.ToLower(strings.Trim(line, "[] \t"))
			switch name {
			case "interface":
				section = "interface"
			case "peer":
				section = "peer"
				conf.peers = append(conf.peers, map[string]string{})
			default:
				section = "" // unknown section: ignore its keys
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("wg-quick line %d: not a key = value line", i+1)
		}
		key := strings.ToLower(strings.TrimSpace(k))
		val := strings.TrimSpace(v)
		switch section {
		case "interface":
			conf.iface[key] = val
		case "peer":
			conf.peers[len(conf.peers)-1][key] = val
		}
	}
	if len(conf.iface) == 0 && len(conf.peers) == 0 {
		return nil, fmt.Errorf("wg-quick conf has no [Interface] or [Peer] section")
	}
	return conf, nil
}

// wgPeerOut is one converted [Peer] section.
type wgPeerOut struct {
	server string
	port   int
	pub    string
	ips    []string
	psk    string
	keep   int
}

// decodeWGQuick converts a wg-quick .conf into one mihomo wireguard node
// (simplified syntax for a single peer, full peers syntax for more).
func decodeWGQuick(text string) (*DecodeResult, error) {
	conf, err := parseWGQuick(text)
	if err != nil {
		return nil, err
	}
	priv := conf.iface["privatekey"]
	if priv == "" {
		return nil, fmt.Errorf("wg-quick conf: [Interface] has no PrivateKey")
	}
	if !validB64Key(priv, 32) {
		return nil, fmt.Errorf("wg-quick conf: invalid PrivateKey (want 32-byte base64)")
	}

	var warns []string
	peers := make([]wgPeerOut, 0, len(conf.peers))
	seenIPs := map[string]bool{}
	for i, p := range conf.peers {
		where := fmt.Sprintf("peer %d", i+1)
		host, port, err := wgEndpoint(p["endpoint"])
		if err != nil {
			warns = append(warns, fmt.Sprintf("skipped %s: %v", where, err))
			continue
		}
		if host == "" {
			warns = append(warns, fmt.Sprintf("skipped %s: missing host in Endpoint", where))
			continue
		}
		pub := p["publickey"]
		if pub == "" || !validB64Key(pub, 32) {
			warns = append(warns, fmt.Sprintf("skipped %s: missing or invalid PublicKey", where))
			continue
		}
		ips := wgAllowedIPs(p["allowedips"])
		if key := strings.Join(ips, ","); seenIPs[key] {
			return nil, fmt.Errorf("wg-quick conf: peer %d repeats the AllowedIPs of an earlier peer (mihomo requires distinct peers)", i+1)
		} else {
			seenIPs[key] = true
		}
		out := wgPeerOut{server: host, port: port, pub: pub, ips: ips}
		if psk := p["presharedkey"]; psk != "" {
			if !validB64Key(psk, 32) {
				warns = append(warns, fmt.Sprintf("skipped %s: invalid PresharedKey", where))
				continue
			}
			out.psk = psk
		}
		if ka := p["persistentkeepalive"]; ka != "" && ka != "off" {
			if n, err := strconv.Atoi(ka); err == nil && n > 0 && n <= 65535 {
				out.keep = n
			} else if err != nil {
				warns = append(warns, fmt.Sprintf("%s: ignoring bad PersistentKeepalive %q", where, ka))
			}
		}
		peers = append(peers, out)
	}
	if len(peers) == 0 {
		if len(warns) > 0 {
			return nil, fmt.Errorf("wg-quick conf has no usable peers (%s)", warns[0])
		}
		return nil, fmt.Errorf("wg-quick conf has no [Peer] section")
	}

	// [Interface] fields, shared by both syntaxes.
	var ip, ipv6 string
	for _, a := range splitWGList(conf.iface["address"]) {
		a = wgCIDR(a)
		if strings.Contains(a, ":") {
			if ipv6 == "" {
				ipv6 = a
			}
		} else if ip == "" {
			ip = a
		}
	}
	if ip == "" && ipv6 == "" {
		return nil, fmt.Errorf("wg-quick conf: [Interface] has no Address")
	}
	mtu := 0
	if s := conf.iface["mtu"]; s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			mtu = n
		} else {
			warns = append(warns, fmt.Sprintf("ignoring bad MTU %q", s))
		}
	}
	dns := splitWGList(conf.iface["dns"])
	amnezia := wgAmneziaOpts(conf.iface)

	// Emission follows the field order of the mihomo wiki examples.
	m := newOMap().Set("name", peers[0].server).Set("type", "wireguard")
	if len(peers) == 1 {
		m.Set("server", peers[0].server).Set("port", peers[0].port)
	}
	if ip != "" {
		m.Set("ip", ip)
	}
	if ipv6 != "" {
		m.Set("ipv6", ipv6)
	}
	m.Set("private-key", priv)
	if len(peers) == 1 {
		p := &peers[0]
		m.Set("public-key", p.pub).Set("allowed-ips", p.ips)
		if p.psk != "" {
			m.Set("pre-shared-key", p.psk)
		}
		if p.keep > 0 {
			m.Set("persistent-keepalive", p.keep)
		}
	} else {
		list := make([]*yamlx.OMap, 0, len(peers))
		for _, p := range peers {
			e := newOMap().
				Set("server", p.server).
				Set("port", p.port).
				Set("public-key", p.pub).
				Set("allowed-ips", p.ips)
			if p.psk != "" {
				e.Set("pre-shared-key", p.psk)
			}
			list = append(list, e)
		}
		m.Set("peers", list)
	}
	m.Set("udp", true)
	if mtu > 0 {
		m.Set("mtu", mtu)
	}
	if len(dns) > 0 {
		m.Set("remote-dns-resolve", true)
		m.Set("dns", dns)
	}
	if amnezia != nil {
		m.Set("amnezia-wg-option", amnezia)
	}

	nodes := []Node{{Name: peers[0].server, Raw: m}}
	var sanitizeWarns []string
	nodes, sanitizeWarns = sanitizeNodes(nodes)
	warns = append(warns, sanitizeWarns...)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("wireguard node failed validation (%s)", sanitizeWarns[0])
	}
	dedupeNames(&nodes)
	return &DecodeResult{Parsed: &ParsedContent{Kind: KindNodes, Nodes: nodes}, Warnings: warns}, nil
}

// wgEndpoint splits a wg-quick Endpoint value ("host", "host:port" or
// "[v6]:port") into host and port. A bare host defaults to the WireGuard
// port 51820; an out-of-range or non-numeric port is an error.
func wgEndpoint(v string) (string, int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", 0, fmt.Errorf("missing Endpoint")
	}
	if strings.HasPrefix(v, "[") {
		host, rest, _ := strings.Cut(v, "]")
		port := 51820
		if s := strings.TrimPrefix(rest, ":"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 || n > 65535 {
				return "", 0, fmt.Errorf("invalid port %q in Endpoint", s)
			}
			port = n
		}
		return host[1:], port, nil
	}
	host, s, ok := strings.Cut(v, ":")
	if !ok {
		return host, 51820, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return "", 0, fmt.Errorf("invalid port %q in Endpoint", s)
	}
	return host, n, nil
}

// wgAllowedIPs splits a wg-quick AllowedIPs value into its CIDR list; an
// absent or empty value defaults to the full-tunnel 0.0.0.0/0 (the mihomo
// wiki's default recommendation — routing decisions belong to mihomo).
func wgAllowedIPs(v string) []string {
	ips := splitWGList(v)
	if len(ips) == 0 {
		return []string{"0.0.0.0/0"}
	}
	return ips
}

// splitWGList splits a comma-separated wg-quick value ("a, b,c").
func splitWGList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	out := []string{}
	for _, tok := range strings.Split(v, ",") {
		if tok = strings.TrimSpace(tok); tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

// wgCIDR makes a bare interface address a proper prefix the way wg-quick
// documents Address values (a host address means a /32 or /128 route).
func wgCIDR(addr string) string {
	if strings.Contains(addr, "/") {
		return addr
	}
	if strings.Contains(addr, ":") {
		return addr + "/128"
	}
	return addr + "/32"
}

// wgAmneziaKeys maps AmneziaWG [Interface] keys (wiki: AmneziaWG v1.0+)
// onto mihomo's amnezia-wg-option fields.
var wgAmneziaKeys = []struct{ conf, opt string }{
	{"jc", "jc"}, {"jmin", "jmin"}, {"jmax", "jmax"},
	{"s1", "s1"}, {"s2", "s2"}, {"s3", "s3"}, {"s4", "s4"},
	{"h1", "h1"}, {"h2", "h2"}, {"h3", "h3"}, {"h4", "h4"},
	{"i1", "i1"}, {"i2", "i2"}, {"i3", "i3"}, {"i4", "i4"}, {"i5", "i5"},
}

// wgAmneziaOpts collects AmneziaWG parameters from the [Interface] section
// into an amnezia-wg-option map; nil when none are present.
func wgAmneziaOpts(iface map[string]string) *yamlx.OMap {
	var m *yamlx.OMap
	for _, k := range wgAmneziaKeys {
		v, ok := iface[k.conf]
		if !ok || v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			continue
		}
		if m == nil {
			m = newOMap()
		}
		m.Set(k.opt, n)
	}
	return m
}
