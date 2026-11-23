package profile

// wgquick_test.go pins the wg-quick -> mihomo wireguard translation: the
// detector, both emission syntaxes, defaults, AmneziaWG parameters, the
// conservative failure matrix and sanitize acceptance. The integration
// suite (step 7.5) additionally boots the real core with a two-peer conf.

import (
	"strings"
	"testing"
)

const wgPrivKey = "eCtXsJZ27+4PbhDkHnB923tkUn2Gj59wZw5wFA75MnU="
const wgPubKey1 = "Cr8hWlKvtDt7nrvf+f0brNQQzabAqrjfBvas9pmowjo="
const wgPubKey2 = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
const wgPSK = "31aIhAPwktDGpH4JDhA8GNvjFXEf/a6+UaQRyOAiyfM="

// wgList reads a string-list field from a nodeMap result (the YAML
// round-trip turns every list into []any).
func wgList(m map[string]any, k string) []string {
	v, ok := m[k]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s, _ := e.(string)
			out = append(out, s)
		}
		return out
	}
	return nil
}

// wgMapOf re-reads a nested map field (peers entry, amnezia-wg-option).
func wgMapOf(m map[string]any, k string) map[string]any {
	v, ok := m[k].([]any)
	if !ok || len(v) == 0 {
		return nil
	}
	out, _ := v[0].(map[string]any)
	return out
}

func TestLooksLikeWGQuick(t *testing.T) {
	pos := []string{
		"[Interface]\nPrivateKey = " + wgPrivKey + "\n[Peer]\nPublicKey = " + wgPubKey1 + "\n",
		"[interface]\nprivatekey = " + wgPrivKey + "\n[peer]\npublickey = " + wgPubKey1 + "\n",
		"[Interface]\nAddress = 10.2.0.2/32\nPrivateKey = " + wgPrivKey + "\n",
	}
	for i, in := range pos {
		if !looksLikeWGQuick(in) {
			t.Errorf("pos[%d]: detector rejected a wg-quick conf", i)
		}
		if !looksLikeWGQuick(in) { // stability
			t.Errorf("pos[%d]: detector is not deterministic", i)
		}
	}
	neg := []string{
		"proxies:\n  - {name: n, type: socks5, server: 1.2.3.4, port: 1}\n",
		`{"outbounds": [{"type": "socks", "tag": "a"}]}`,
		"vmess://eyJ2IjoiMiIsInBzIjoibiIsImFkZCI6IjEuMi4zLjQiLCJwb3J0IjoiNDQzIn0=",
		"ss://YWVzLTI1Ni1nY206cGFzcw==@1.2.3.4:443#node",
		"just some text\nwith lines",
		"",
	}
	for i, in := range neg {
		if looksLikeWGQuick(in) {
			t.Errorf("neg[%d]: detector accepted non-wg-quick input", i)
		}
	}
}

func TestDecodeWGQuickSinglePeer(t *testing.T) {
	conf := "[Interface]\n" +
		"Address = 172.16.0.2/32, fd01:5ca1:ab1e:80fa:ab85:6eea:213f:f4a5/128\n" +
		"PrivateKey = " + wgPrivKey + "\n" +
		"DNS = 1.1.1.1, 8.8.8.8\n" +
		"MTU = 1408\n" +
		"Table = off\n" +
		"[Peer]\n" +
		"Endpoint = 162.159.192.1:2480\n" +
		"PublicKey = " + wgPubKey1 + "\n" +
		"AllowedIPs = 0.0.0.0/0, ::/0\n" +
		"PresharedKey = " + wgPSK + "\n" +
		"PersistentKeepalive = 25\n"
	dec, err := decodeWGQuick(conf)
	if err != nil {
		t.Fatal(err)
	}
	if len(dec.Parsed.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(dec.Parsed.Nodes))
	}
	m := nodeMap(t, &dec.Parsed.Nodes[0])
	for k, want := range map[string]any{
		"name": "162.159.192.1", "type": "wireguard",
		"server": "162.159.192.1", "port": 2480,
		"ip": "172.16.0.2/32", "ipv6": "fd01:5ca1:ab1e:80fa:ab85:6eea:213f:f4a5/128",
		"private-key": wgPrivKey, "public-key": wgPubKey1,
		"pre-shared-key": wgPSK, "persistent-keepalive": 25,
		"udp": true, "mtu": 1408, "remote-dns-resolve": true,
	} {
		if m[k] != want {
			t.Errorf("%s = %v (%T), want %v", k, m[k], m[k], want)
		}
	}
	if ips := wgList(m, "allowed-ips"); len(ips) != 2 || ips[0] != "0.0.0.0/0" || ips[1] != "::/0" {
		t.Errorf("allowed-ips = %#v", m["allowed-ips"])
	}
	if dns := wgList(m, "dns"); len(dns) != 2 || dns[0] != "1.1.1.1" || dns[1] != "8.8.8.8" {
		t.Errorf("dns = %#v", m["dns"])
	}
	if _, has := m["peers"]; has {
		t.Errorf("single peer must use the simplified syntax, got peers: %v", m["peers"])
	}
	if _, has := m["table"]; has {
		t.Errorf("wg-quick plumbing leaked into the node: table = %v", m["table"])
	}
	if dec.Parsed.Kind != KindNodes {
		t.Errorf("kind = %v, want nodes", dec.Parsed.Kind)
	}
}

func TestDecodeWGQuickDefaults(t *testing.T) {
	conf := "[Interface]\n" +
		"Address = 10.2.0.2/32\n" +
		"PrivateKey = " + wgPrivKey + "\n" +
		"[Peer]\n" +
		"Endpoint = wg.example.com\n" +
		"PublicKey = " + wgPubKey1 + "\n"
	dec, err := decodeWGQuick(conf)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, &dec.Parsed.Nodes[0])
	if m["server"] != "wg.example.com" || m["port"] != 51820 {
		t.Errorf("endpoint = %v:%v, want wg.example.com:51820", m["server"], m["port"])
	}
	if ips := wgList(m, "allowed-ips"); len(ips) != 1 || ips[0] != "0.0.0.0/0" {
		t.Errorf("allowed-ips = %#v, want default 0.0.0.0/0", m["allowed-ips"])
	}
	for _, k := range []string{"ipv6", "mtu", "remote-dns-resolve", "dns", "pre-shared-key", "persistent-keepalive"} {
		if _, has := m[k]; has {
			t.Errorf("absent [Interface]/[Peer] field leaked: %s = %v", k, m[k])
		}
	}
}

func TestDecodeWGQuickIPv6(t *testing.T) {
	conf := "[Interface]\n" +
		"Address = fd00:168:2::2\n" + // bare v6 -> /128
		"PrivateKey = " + wgPrivKey + "\n" +
		"[Peer]\n" +
		"Endpoint = [2001:db8::1]:2408\n" +
		"PublicKey = " + wgPubKey1 + "\n" +
		"AllowedIPs = ::/0\n"
	dec, err := decodeWGQuick(conf)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, &dec.Parsed.Nodes[0])
	if m["server"] != "2001:db8::1" || m["port"] != 2408 {
		t.Errorf("endpoint = %v:%v, want [2001:db8::1]:2408 unbracketed", m["server"], m["port"])
	}
	if m["ipv6"] != "fd00:168:2::2/128" {
		t.Errorf("ipv6 = %v, want fd00:168:2::2/128", m["ipv6"])
	}
	if _, has := m["ip"]; has {
		t.Errorf("ip = %v, want absent for a v6-only interface", m["ip"])
	}
	if m["name"] != "2001:db8::1" {
		t.Errorf("name = %v, want the endpoint host", m["name"])
	}
}

func TestDecodeWGQuickMultiPeer(t *testing.T) {
	conf := "[Interface]\n" +
		"Address = 10.2.0.2/32, fd00:168:2::2/128\n" +
		"PrivateKey = " + wgPrivKey + "\n" +
		"DNS = 1.1.1.1\n" +
		"MTU = 1420\n" +
		"[Peer]\n" +
		"Endpoint = 162.159.192.1:2480\n" +
		"PublicKey = " + wgPubKey1 + "\n" +
		"AllowedIPs = 10.100.0.0/16, 10.200.0.0/16\n" +
		"PresharedKey = " + wgPSK + "\n" +
		"[Peer]\n" +
		"Endpoint = wg2.example.com:51821\n" +
		"PublicKey = " + wgPubKey2 + "\n" +
		"AllowedIPs = 0.0.0.0/0, ::/0\n" +
		"PersistentKeepalive = 25\n"
	dec, err := decodeWGQuick(conf)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, &dec.Parsed.Nodes[0])
	for _, k := range []string{"server", "port", "public-key"} {
		if _, has := m[k]; has {
			t.Errorf("full syntax must not carry top-level %s (ignored by mihomo)", k)
		}
	}
	if m["name"] != "162.159.192.1" {
		t.Errorf("name = %v, want first peer's server", m["name"])
	}
	peers, ok := m["peers"].([]any)
	if !ok || len(peers) != 2 {
		t.Fatalf("peers = %#v, want 2 entries", m["peers"])
	}
	p1, _ := peers[0].(map[string]any)
	p2, _ := peers[1].(map[string]any)
	if p1["server"] != "162.159.192.1" || p1["port"] != 2480 || p1["public-key"] != wgPubKey1 {
		t.Errorf("peer1 = %#v", p1)
	}
	if ips, _ := p1["allowed-ips"].([]any); len(ips) != 2 || ips[0] != "10.100.0.0/16" {
		t.Errorf("peer1 allowed-ips = %#v", p1["allowed-ips"])
	}
	if p1["pre-shared-key"] != wgPSK {
		t.Errorf("peer1 pre-shared-key = %v", p1["pre-shared-key"])
	}
	if p2["server"] != "wg2.example.com" || p2["port"] != 51821 || p2["public-key"] != wgPubKey2 {
		t.Errorf("peer2 = %#v", p2)
	}
	if ips, _ := p2["allowed-ips"].([]any); len(ips) != 2 || ips[1] != "::/0" {
		t.Errorf("peer2 allowed-ips = %#v", p2["allowed-ips"])
	}
	if _, has := p2["persistent-keepalive"]; has {
		t.Errorf("peers list has no keepalive field in mihomo; must not be emitted")
	}
	if m["mtu"] != 1420 || m["remote-dns-resolve"] != true {
		t.Errorf("interface fields lost in full syntax: mtu=%v remote-dns-resolve=%v", m["mtu"], m["remote-dns-resolve"])
	}
}

func TestDecodeWGQuickPartialSkip(t *testing.T) {
	conf := "[Interface]\n" +
		"Address = 10.2.0.2/32\n" +
		"PrivateKey = " + wgPrivKey + "\n" +
		"[Peer]\n" +
		"Endpoint = good.example.com:51820\n" +
		"PublicKey = " + wgPubKey1 + "\n" +
		"[Peer]\n" +
		"Endpoint = bad.example.com:51820\n" +
		"PublicKey = not-a-key\n" +
		"[Peer]\n" +
		"Endpoint = also-good.example.com:51820\n" +
		"PublicKey = " + wgPubKey2 + "\n" +
		"AllowedIPs = 10.0.0.0/8\n"
	dec, err := decodeWGQuick(conf)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, &dec.Parsed.Nodes[0])
	peers, _ := m["peers"].([]any)
	if len(peers) != 2 {
		t.Fatalf("peers = %d, want the 2 convertible of 3", len(peers))
	}
	if len(dec.Warnings) == 0 || !strings.Contains(dec.Warnings[0], "peer 2") {
		t.Errorf("warnings = %#v, want a peer 2 skip note", dec.Warnings)
	}
}

func TestDecodeWGQuickAmnezia(t *testing.T) {
	conf := "[Interface]\n" +
		"Address = 10.5.0.2/32\n" +
		"PrivateKey = " + wgPrivKey + "\n" +
		"Jc = 3\nJmin = 50\nJmax = 1000\n" +
		"S1 = 30\nS2 = 40\nS3 = 50\nS4 = 8\n" +
		"H1 = 123456\nH2 = 67543\nH3 = 123123\nH4 = 32345\n" +
		"I1 = 111\nI2 = 222\nI3 = 333\nI4 = 444\nI5 = 555\n" +
		"Bogus = 9\n" +
		"[Peer]\n" +
		"Endpoint = 162.159.192.1:2480\n" +
		"PublicKey = " + wgPubKey1 + "\n"
	dec, err := decodeWGQuick(conf)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, &dec.Parsed.Nodes[0])
	opt, ok := m["amnezia-wg-option"].(map[string]any)
	if !ok {
		t.Fatalf("amnezia-wg-option = %#v", m["amnezia-wg-option"])
	}
	for k, want := range map[string]any{
		"jc": 3, "jmin": 50, "jmax": 1000,
		"s1": 30, "s2": 40, "s3": 50, "s4": 8,
		"h1": 123456, "h2": 67543, "h3": 123123, "h4": 32345,
		"i1": 111, "i2": 222, "i3": 333, "i4": 444, "i5": 555,
	} {
		if opt[k] != want {
			t.Errorf("amnezia %s = %v, want %v", k, opt[k], want)
		}
	}
	if _, has := opt["bogus"]; has {
		t.Errorf("unknown AmneziaWG key leaked: %v", opt["bogus"])
	}
}

func TestDecodeWGQuickFailures(t *testing.T) {
	cases := []struct {
		name, conf, want string
	}{
		{"no private key", "[Interface]\nAddress = 10.2.0.2/32\n[Peer]\nEndpoint = a:1\nPublicKey = " + wgPubKey1 + "\n", "PrivateKey"},
		{"invalid private key", "[Interface]\nAddress = 10.2.0.2/32\nPrivateKey = nope\n[Peer]\nEndpoint = a:1\nPublicKey = " + wgPubKey1 + "\n", "PrivateKey"},
		{"no address", "[Interface]\nPrivateKey = " + wgPrivKey + "\n[Peer]\nEndpoint = a:1\nPublicKey = " + wgPubKey1 + "\n", "Address"},
		{"no peers", "[Interface]\nAddress = 10.2.0.2/32\nPrivateKey = " + wgPrivKey + "\n", "Peer"},
		{"all peers broken", "[Interface]\nAddress = 10.2.0.2/32\nPrivateKey = " + wgPrivKey + "\n[Peer]\nEndpoint = a:99999\nPublicKey = " + wgPubKey1 + "\n[Peer]\nEndpoint = b:1\nPublicKey = bad\n", "no usable peers"},
		{"garbage line", "[Interface]\nAddress = 10.2.0.2/32\nPrivateKey = " + wgPrivKey + "\nwhat is this line\n[Peer]\nEndpoint = a:1\nPublicKey = " + wgPubKey1 + "\n", "key = value"},
		{"not a conf", "hello world", "key = value"},
	}
	for _, tc := range cases {
		_, err := decodeWGQuick(tc.conf)
		if err == nil {
			t.Errorf("%s: import unexpectedly succeeded", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q, want it to mention %q", tc.name, err, tc.want)
		}
	}
}

func TestDecodeWGQuickDuplicateAllowedIPs(t *testing.T) {
	conf := "[Interface]\n" +
		"Address = 10.2.0.2/32\n" +
		"PrivateKey = " + wgPrivKey + "\n" +
		"[Peer]\n" +
		"Endpoint = a.example.com:51820\n" +
		"PublicKey = " + wgPubKey1 + "\n" +
		"AllowedIPs = 0.0.0.0/0\n" +
		"[Peer]\n" +
		"Endpoint = b.example.com:51820\n" +
		"PublicKey = " + wgPubKey2 + "\n" +
		"AllowedIPs = 0.0.0.0/0\n"
	_, err := decodeWGQuick(conf)
	if err == nil || !strings.Contains(err.Error(), "distinct") {
		t.Fatalf("err = %v, want duplicate allowed-ips rejection", err)
	}
}

func TestSanitizeWireguardFullSyntax(t *testing.T) {
	// The full (multi-peer) syntax carries no top-level server/port — the
	// generic sanitize gate must not drop it (wireguard branch handles it).
	conf := "[Interface]\n" +
		"Address = 10.2.0.2/32\n" +
		"PrivateKey = " + wgPrivKey + "\n" +
		"[Peer]\n" +
		"Endpoint = 162.159.192.1:2480\n" +
		"PublicKey = " + wgPubKey1 + "\n" +
		"[Peer]\n" +
		"Endpoint = wg2.example.com:51821\n" +
		"PublicKey = " + wgPubKey2 + "\n" +
		"AllowedIPs = ::/0\n"
	dec, err := decodeWGQuick(conf)
	if err != nil {
		t.Fatal(err)
	}
	if len(dec.Parsed.Nodes) != 1 {
		t.Fatalf("sanitize dropped the multi-peer wireguard node: %d nodes", len(dec.Parsed.Nodes))
	}
	if issue := nodeIssue(dec.Parsed.Nodes[0]); issue != "" {
		t.Errorf("nodeIssue = %q, want acceptance", issue)
	}
}
