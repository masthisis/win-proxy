package profile

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

// validPBK returns a syntactically valid 32-byte urlsafe base64 REALITY
// public key (sanitize.go rejects anything else).
func validPBK() string { return strings.Repeat("A", 43) }

const testUUID = "aaaabbbb-cccc-dddd-eeee-ffff00001111"

// --- detector ----------------------------------------------------------------

func TestLooksLikeSingboxJSON(t *testing.T) {
	yes := []string{
		`{"outbounds":[{"type":"socks","tag":"o","server":"1.2.3.4","server_port":1080}]}`,
		`{ "log": {}, "outbounds": [{"type":"direct","tag":"direct"}], "route": {} }`,
	}
	for _, s := range yes {
		if !looksLikeSingboxJSON(s) {
			t.Errorf("should detect: %s", s)
		}
	}
	no := []string{
		`{"routes":[]}`,
		`[]`,
		`proxies: []`,
		`{"outbounds":[]}`,
		`not json`,
		`vless://x@1.2.3.4:443`,
	}
	for _, s := range no {
		if looksLikeSingboxJSON(s) {
			t.Errorf("should NOT detect: %s", s)
		}
	}
}

// --- vless + REALITY + ws + utls ---------------------------------------------

func TestDecodeSingboxVLESSReality(t *testing.T) {
	cfg := fmt.Sprintf(`{
          "outbounds": [
            {
              "type": "vless", "tag": "Reality Node",
              "server": "5.6.7.8", "server_port": 8443,
              "uuid": %q, "flow": "xtls-rprx-vision",
              "packet_encoding": "xudp",
              "tls": {
                "enabled": true, "server_name": "www.microsoft.com",
                "utls": {"enabled": true, "fingerprint": "safari"},
                "reality": {"enabled": true, "public_key": %q, "short_id": "ab12"}
              },
              "transport": {"type": "ws", "path": "/ws", "headers": {"Host": "cdn.example.com"}}
            }
          ]
        }`, testUUID, validPBK())
	dec, err := decodeSingbox(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(dec.Parsed.Nodes) != 1 {
		t.Fatalf("nodes = %d", len(dec.Parsed.Nodes))
	}
	m := nodeMap(t, &dec.Parsed.Nodes[0])
	for k, want := range map[string]any{
		"name": "Reality Node", "type": "vless", "server": "5.6.7.8",
		"port": 8443, "uuid": testUUID, "flow": "xtls-rprx-vision",
		"tls": true, "servername": "www.microsoft.com",
		"client-fingerprint": "safari", "packet-encoding": "xudp",
		"network": "ws",
	} {
		if m[k] != want {
			t.Errorf("%s = %v, want %v", k, m[k], want)
		}
	}
	re := m["reality-opts"].(map[string]any)
	if re["public-key"] != validPBK() || re["short-id"] != "ab12" {
		t.Errorf("reality = %v", re)
	}
	ws := m["ws-opts"].(map[string]any)
	if ws["path"] != "/ws" {
		t.Errorf("ws path = %v", ws["path"])
	}
	if ws["headers"].(map[string]any)["Host"] != "cdn.example.com" {
		t.Errorf("ws host = %v", ws["headers"])
	}
}

// --- vmess + h2 transport + smux ---------------------------------------------

func TestDecodeSingboxVMess(t *testing.T) {
	cfg := fmt.Sprintf(`{
          "outbounds": [
            {
              "type": "vmess", "tag": "VM-Node",
              "server": "vm.example.com", "server_port": 443,
              "uuid": %q, "security": "aes-128-gcm", "alter_id": 2,
              "tls": {"enabled": true, "server_name": "vm.example.com"},
              "transport": {"type": "http", "path": "/h2", "headers": {"Host": "cdn.vm.com"}},
              "multiplex": {"enabled": true, "protocol": "yamux", "max_connections": 4, "padding": true}
            }
          ]
        }`, testUUID)
	dec, err := decodeSingbox(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, &dec.Parsed.Nodes[0])
	for k, want := range map[string]any{
		"type": "vmess", "server": "vm.example.com", "port": 443,
		"uuid": testUUID, "alterId": 2, "cipher": "aes-128-gcm",
		"tls": true, "network": "h2",
	} {
		if m[k] != want {
			t.Errorf("%s = %v, want %v", k, m[k], want)
		}
	}
	h2 := m["h2-opts"].(map[string]any)
	if h2["path"] != "/h2" {
		t.Errorf("h2 path = %v", h2["path"])
	}
	if host, ok := h2["host"].([]any); !ok || host[0] != "cdn.vm.com" {
		t.Errorf("h2 host = %v", h2["host"])
	}
	sm := m["smux"].(map[string]any)
	if sm["protocol"] != "yamux" || sm["max-connections"] != 4 || sm["padding"] != true {
		t.Errorf("smux = %v", sm)
	}
}

// --- shadowsocks with obfs plugin ---------------------------------------------

func TestDecodeSingboxSS(t *testing.T) {
	cfg := `{
          "outbounds": [
            {
              "type": "shadowsocks", "tag": "SS1",
              "server": "1.2.3.4", "server_port": 8388,
              "method": "2022-blake3-aes-256-gcm", "password": "secret",
              "udp_over_tcp": true,
              "plugin": "obfs",
              "plugin_opts": {"mode": "http", "host": "bing.com"}
            }
          ]
        }`
	dec, err := decodeSingbox(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, &dec.Parsed.Nodes[0])
	if m["cipher"] != "2022-blake3-aes-256-gcm" || m["password"] != "secret" {
		t.Fatalf("ss fields = %v", m)
	}
	if m["udp-over-tcp"] != true {
		t.Errorf("udp-over-tcp missing")
	}
	po := m["plugin-opts"].(map[string]any)
	if m["plugin"] != "obfs" || po["mode"] != "http" || po["host"] != "bing.com" {
		t.Errorf("plugin = %v / %v", m["plugin"], po)
	}
}

// --- hysteria2: obfs, port hopping, up/down -----------------------------------

func TestDecodeSingboxHysteria2(t *testing.T) {
	cfg := `{
          "outbounds": [
            {
              "type": "hysteria2", "tag": "HY2",
              "server": "hy.example.com", "server_port": 443,
              "password": "pw", "up_mbps": 30, "down_mbps": 200,
              "server_ports": ["20000:30000"],
              "obfs": {"type": "salamander", "password": "obfspw"},
              "tls": {"enabled": true, "server_name": "hy.example.com", "insecure": true}
            }
          ]
        }`
	dec, err := decodeSingbox(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, &dec.Parsed.Nodes[0])
	for k, want := range map[string]any{
		"type": "hysteria2", "port": 443, "password": "pw",
		"up": 30, "down": 200, "obfs": "salamander",
		"obfs-password": "obfspw", "sni": "hy.example.com",
		"skip-cert-verify": true,
	} {
		if m[k] != want {
			t.Errorf("%s = %v, want %v", k, m[k], want)
		}
	}
	if m["ports"] != "20000-30000" {
		t.Errorf("ports = %v", m["ports"])
	}
}

// --- trojan + grpc -------------------------------------------------------------

func TestDecodeSingboxTrojan(t *testing.T) {
	cfg := `{
          "outbounds": [
            {
              "type": "trojan", "tag": "TR",
              "server": "9.9.9.9", "server_port": 443, "password": "p@ss",
              "tls": {"enabled": true, "server_name": "t.example.com", "alpn": ["h2"]},
              "transport": {"type": "grpc", "service_name": "svc"}
            }
          ]
        }`
	dec, err := decodeSingbox(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, &dec.Parsed.Nodes[0])
	if m["password"] != "p@ss" || m["sni"] != "t.example.com" {
		t.Fatalf("trojan fields = %v", m)
	}
	if m["alpn"].([]any)[0] != "h2" {
		t.Errorf("alpn = %v", m["alpn"])
	}
	if m["grpc-opts"].(map[string]any)["grpc-service-name"] != "svc" {
		t.Errorf("grpc = %v", m["grpc-opts"])
	}
}

// --- tuic ----------------------------------------------------------------------

func TestDecodeSingboxTUIC(t *testing.T) {
	cfg := fmt.Sprintf(`{
          "outbounds": [
            {
              "type": "tuic", "tag": "TU",
              "server": "tu.example.com", "server_port": 443,
              "uuid": %q, "password": "tupw",
              "congestion_control": "bbr", "udp_relay_mode": "native",
              "tls": {"enabled": true, "server_name": "tu.example.com", "alpn": ["h3"]}
            }
          ]
        }`, testUUID)
	dec, err := decodeSingbox(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, &dec.Parsed.Nodes[0])
	for k, want := range map[string]any{
		"type": "tuic", "uuid": testUUID, "password": "tupw",
		"congestion-controller": "bbr", "udp-relay-mode": "native", "sni": "tu.example.com",
	} {
		if m[k] != want {
			t.Errorf("%s = %v, want %v", k, m[k], want)
		}
	}
}

// --- wireguard (incl. reserved as base64) ----------------------------------------

func TestDecodeSingboxWireguard(t *testing.T) {
	// base64(std) of bytes [209, 98, 59]
	reserved := base64.StdEncoding.EncodeToString([]byte{209, 98, 59})
	cfg := fmt.Sprintf(`{
          "outbounds": [
            {
              "type": "wireguard", "tag": "WG",
              "server": "162.159.192.1", "server_port": 2480,
              "local_address": ["172.16.0.2/32", "fd01:5ca1:ab1e:80fa:ab85:6eea:213f:f4a5/128"],
              "private_key": "eCtXsJZ27+4PbhDkHnB923tkUn2Gj59wZw5wFA75MnU=",
              "peer_public_key": "Cr8hWlKvtDt7nrvf+f0brNQQzabAqrjfBvas9pmowjo=",
              "pre_shared_key": "31aIhAPwktDGpH4JDhA8GNvjFXEf/a6+UaQRyOAiyfM=",
              "reserved": %q, "mtu": 1408
            }
          ]
        }`, reserved)
	dec, err := decodeSingbox(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, &dec.Parsed.Nodes[0])
	for k, want := range map[string]any{
		"type": "wireguard", "server": "162.159.192.1", "port": 2480, "mtu": 1408,
		"ip": "172.16.0.2/32", "ipv6": "fd01:5ca1:ab1e:80fa:ab85:6eea:213f:f4a5/128",
	} {
		if m[k] != want {
			t.Errorf("%s = %v, want %v", k, m[k], want)
		}
	}
	if got := fmt.Sprint(m["reserved"]); got != "[209 98 59]" {
		t.Errorf("reserved = %v", m["reserved"])
	}
	if m["pre-shared-key"] == nil || m["public-key"] == nil {
		t.Errorf("wg keys missing: %v", m)
	}
}

// --- socks + http ----------------------------------------------------------------

func TestDecodeSingboxSocksAndHTTP(t *testing.T) {
	cfg := `{
          "outbounds": [
            {"type": "socks", "tag": "S5", "server": "10.0.0.2", "server_port": 1080,
             "version": "5", "username": "u", "password": "p"},
            {"type": "http", "tag": "H1", "server": "10.0.0.3", "server_port": 8080,
             "username": "u2", "password": "p2",
             "tls": {"enabled": true, "server_name": "corp.local", "insecure": true}}
          ]
        }`
	dec, err := decodeSingbox(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(dec.Parsed.Nodes) != 2 {
		t.Fatalf("nodes = %d", len(dec.Parsed.Nodes))
	}
	m1 := nodeMap(t, &dec.Parsed.Nodes[0])
	if m1["type"] != "socks5" || m1["username"] != "u" || m1["udp"] != true {
		t.Errorf("socks = %v", m1)
	}
	m2 := nodeMap(t, &dec.Parsed.Nodes[1])
	if m2["type"] != "http" || m2["tls"] != true || m2["skip-cert-verify"] != true {
		t.Errorf("http = %v", m2)
	}
}

// --- skip counting: groups and unsupported types -----------------------------------

func TestDecodeSingboxSkipsGroups(t *testing.T) {
	cfg := `{
          "outbounds": [
            {"type": "selector", "tag": "Choose", "outbounds": ["A", "B"]},
            {"type": "urltest", "tag": "Auto", "outbounds": ["A", "B"]},
            {"type": "direct", "tag": "direct"},
            {"type": "block", "tag": "block"},
            {"type": "dns", "tag": "dns-out"},
            {"type": "shadowtls", "tag": "stl", "server": "1.1.1.1", "server_port": 443, "password": "x"},
            {"type": "vless", "tag": "v1", "server": "1.2.3.4", "server_port": 443,
             "uuid": "aaaabbbb-cccc-dddd-eeee-ffff00001111"}
          ]
        }`
	dec, err := decodeSingbox(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(dec.Parsed.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(dec.Parsed.Nodes))
	}
	if len(dec.Warnings) != 6 {
		t.Fatalf("warnings = %d, want 6: %v", len(dec.Warnings), dec.Warnings)
	}
	for i, w := range dec.Warnings[:4] {
		if !strings.Contains(w, "unsupported outbound type") {
			t.Errorf("warning[%d] = %q", i, w)
		}
	}
}

// --- detour chains ------------------------------------------------------------------

func TestDecodeSingboxDetour(t *testing.T) {
	cfg := fmt.Sprintf(`{
          "outbounds": [
            {"type": "vmess", "tag": "chained", "server": "1.2.3.4", "server_port": 443,
             "uuid": %q, "detour": "hop"},
            {"type": "socks", "tag": "hop", "server": "5.5.5.5", "server_port": 1080},
            {"type": "vless", "tag": "orphan", "server": "6.6.6.6", "server_port": 443,
             "uuid": %q, "detour": "stl"},
            {"type": "shadowtls", "tag": "stl", "server": "7.7.7.7", "server_port": 443, "password": "x"}
          ]
        }`, testUUID, testUUID)
	dec, err := decodeSingbox(cfg)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]map[string]any{}
	for i := range dec.Parsed.Nodes {
		byName[dec.Parsed.Nodes[i].Name] = nodeMap(t, &dec.Parsed.Nodes[i])
	}
	if len(byName) != 2 {
		t.Fatalf("nodes = %v", namesOf(dec))
	}
	if byName["chained"]["dialer-proxy"] != "hop" {
		t.Errorf("dialer-proxy = %v", byName["chained"]["dialer-proxy"])
	}
	if _, ok := byName["orphan"]; ok {
		t.Errorf("orphan (detour skipped) must be dropped")
	}
	if !strings.Contains(strings.Join(dec.Warnings, "; "), `detour "stl"`) {
		t.Errorf("detour warning missing: %v", dec.Warnings)
	}
}

// --- all unusable -> error ------------------------------------------------------------

func TestDecodeSingboxAllUnusable(t *testing.T) {
	cfg := `{"outbounds":[{"type":"selector","tag":"g","outbounds":[]}]}`
	_, err := decodeSingbox(cfg)
	if err == nil || !strings.Contains(err.Error(), "no usable sing-box outbounds") {
		t.Fatalf("err = %v", err)
	}
}

// --- integration with DecodeContent (raw + base64) -----------------------------------

func TestDecodeContentDetectsSingbox(t *testing.T) {
	raw := `{"outbounds":[{"type":"trojan","tag":"TR","server":"9.9.9.9","server_port":443,"password":"pw"}]}`
	dec, err := DecodeContent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Parsed.Kind != KindNodes || len(dec.Parsed.Nodes) != 1 {
		t.Fatalf("kind=%v nodes=%d", dec.Parsed.Kind, len(dec.Parsed.Nodes))
	}
	if dec.Parsed.Nodes[0].Name != "TR" {
		t.Errorf("name = %q", dec.Parsed.Nodes[0].Name)
	}

	b64 := base64.StdEncoding.EncodeToString([]byte(raw))
	dec2, err := DecodeContent(b64)
	if err != nil {
		t.Fatalf("base64: %v", err)
	}
	if dec2.Parsed.Kind != KindNodes || len(dec2.Parsed.Nodes) != 1 {
		t.Fatalf("b64 kind=%v nodes=%d", dec2.Parsed.Kind, len(dec2.Parsed.Nodes))
	}
}

func namesOf(dec *DecodeResult) []string {
	out := make([]string, 0, len(dec.Parsed.Nodes))
	for _, n := range dec.Parsed.Nodes {
		out = append(out, n.Name)
	}
	return out
}
