package profile

import (
	"encoding/base64"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// nodeMap marshals a parsed node back into a map for assertions.
func nodeMap(t *testing.T, n *Node) map[string]any {
	t.Helper()
	data, err := yaml.Marshal(n.Raw)
	if err != nil {
		t.Fatalf("marshal node: %v", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal node: %v", err)
	}
	return m
}

func b64std(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// --- vmess -----------------------------------------------------------------

func TestParseVMess(t *testing.T) {
	json := `{"v":"2","ps":"Tokyo Node","add":"1.2.3.4","port":"443","id":"aaaabbbb-cccc-dddd-eeee-ffff00001111","aid":"0","scy":"auto","net":"ws","type":"none","host":"cdn.example.com","path":"/ws","tls":"tls","sni":"cdn.example.com","alpn":"h2,http/1.1","fp":"chrome"}`
	link := "vmess://" + b64std(json)
	n, err := parseShareLink(link)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, n)
	for k, want := range map[string]any{
		"name": "Tokyo Node", "type": "vmess", "server": "1.2.3.4",
		"port": 443, "uuid": "aaaabbbb-cccc-dddd-eeee-ffff00001111",
		"cipher": "auto", "tls": true, "servername": "cdn.example.com",
		"network": "ws", "client-fingerprint": "chrome",
	} {
		if m[k] != want {
			t.Errorf("%s = %v, want %v", k, m[k], want)
		}
	}
	ws := m["ws-opts"].(map[string]any)
	if ws["path"] != "/ws" {
		t.Errorf("ws path = %v", ws["path"])
	}
	if ws["headers"].(map[string]any)["Host"] != "cdn.example.com" {
		t.Errorf("ws host header missing")
	}
}

// --- vless REALITY ----------------------------------------------------------

func TestParseVLESSReality(t *testing.T) {
	link := "vless://uuid-1234@5.6.7.8:8443?encryption=none&security=reality&sni=www.microsoft.com&fp=chrome&pbk=PUBKEY123&sid=ab12&flow=xtls-rprx-vision&type=tcp#Reality%20Node"
	n, err := parseShareLink(link)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, n)
	if m["type"] != "vless" || m["server"] != "5.6.7.8" || m["port"] != 8443 {
		t.Fatalf("basic fields wrong: %v", m)
	}
	if m["flow"] != "xtls-rprx-vision" {
		t.Errorf("flow = %v", m["flow"])
	}
	re := m["reality-opts"].(map[string]any)
	if re["public-key"] != "PUBKEY123" || re["short-id"] != "ab12" {
		t.Errorf("reality opts = %v", re)
	}
	if m["tls"] != true {
		t.Errorf("tls must be true for reality")
	}
	if m["client-fingerprint"] != "chrome" {
		t.Errorf("fp = %v", m["client-fingerprint"])
	}
}

// --- vless ws ----------------------------------------------------------------

func TestParseVLESSWs(t *testing.T) {
	link := "vless://u1@ex.com:2053?type=ws&security=tls&path=%2Fpath&host=ex.cdn#WS"
	n, err := parseShareLink(link)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, n)
	if m["network"] != "ws" {
		t.Fatalf("network = %v", m["network"])
	}
	ws := m["ws-opts"].(map[string]any)
	if ws["path"] != "/path" {
		t.Errorf("path = %v", ws["path"])
	}
}

// --- vless post-quantum encryption (#90) -------------------------------------

func TestParseVLESSEncryption(t *testing.T) {
	// Real-world shape of an mlkem768x25519plus link (payloads replaced).
	link := "vless://0d01fc6c-edb8-407c-8ee3-f3b5e5b83c06@ex.com:25565?encryption=mlkem768x25519plus.native.0rtt.dQbYeJPo4efEGu8T4Gy-A_GIFQcUik9J9LhlksRcNEU&type=tcp&security=none&headerType=http&host=render.com&path=%2F#PQ"
	n, err := parseShareLink(link)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, n)
	if m["encryption"] != "mlkem768x25519plus.native.0rtt.dQbYeJPo4efEGu8T4Gy-A_GIFQcUik9J9LhlksRcNEU" {
		t.Fatalf("encryption must pass through verbatim, got %v", m["encryption"])
	}
	// HTTP camouflage transport must survive alongside.
	if m["network"] != "http" {
		t.Errorf("network = %v, want http (headerType=http)", m["network"])
	}
	httpOpts := m["http-opts"].(map[string]any)
	if path, ok := httpOpts["path"].([]any); !ok || len(path) != 1 || path[0] != "/" {
		t.Errorf("http-opts path = %v", httpOpts["path"])
	}
}

func TestParseVLESSEncryptionNoneOmitted(t *testing.T) {
	link := "vless://u1@ex.com:443?encryption=none&security=tls&sni=a.example&fp=chrome&type=ws&host=cdn.example&path=%2F#WS"
	n, err := parseShareLink(link)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, n)
	if _, ok := m["encryption"]; ok {
		t.Errorf("encryption=none must not emit an encryption field, got %v", m["encryption"])
	}
	if m["client-fingerprint"] != "chrome" {
		t.Errorf("fp = %v", m["client-fingerprint"])
	}
}

// --- trojan grpc -------------------------------------------------------------

func TestParseTrojan(t *testing.T) {
	link := "trojan://p%40ss@9.9.9.9:443?sni=t.example.com&type=grpc&serviceName=svc&alpn=h2#TR"
	n, err := parseShareLink(link)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, n)
	if m["type"] != "trojan" || m["password"] != "p@ss" {
		t.Fatalf("basic: %v", m)
	}
	if m["sni"] != "t.example.com" {
		t.Errorf("sni = %v", m["sni"])
	}
	grpc := m["grpc-opts"].(map[string]any)
	if grpc["grpc-service-name"] != "svc" {
		t.Errorf("grpc = %v", grpc)
	}
}

// --- ss SIP002 with plugin -----------------------------------------------------

func TestParseSSSIP002(t *testing.T) {
	link := "ss://" + b64std("aes-256-gcm:secretpass") + "@3.3.3.3:8388?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dw.example.com#SSO"
	n, err := parseShareLink(link)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, n)
	if m["cipher"] != "aes-256-gcm" || m["password"] != "secretpass" {
		t.Fatalf("auth: %v", m)
	}
	if m["plugin"] != "obfs" {
		t.Fatalf("plugin = %v", m["plugin"])
	}
	po := m["plugin-opts"].(map[string]any)
	if po["mode"] != "http" || po["host"] != "w.example.com" {
		t.Errorf("plugin opts = %v", po)
	}
}

func TestParseSSLegacy(t *testing.T) {
	link := "ss://" + b64std("chacha20-ietf-poly1305:pw@7.7.7.7:1234") + "#Legacy"
	n, err := parseShareLink(link)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, n)
	if m["server"] != "7.7.7.7" || m["port"] != 1234 || m["cipher"] != "chacha20-ietf-poly1305" {
		t.Fatalf("legacy ss: %v", m)
	}
}

// --- hysteria2 -----------------------------------------------------------------

func TestParseHysteria2(t *testing.T) {
	link := "hy2://letmein@hy.example.com:443?sni=hy.example.com&insecure=1&obfs=salamander&obfs-password=obfspw&mport=443,8000-9000#HY2"
	n, err := parseShareLink(link)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, n)
	if m["type"] != "hysteria2" || m["password"] != "letmein" {
		t.Fatalf("hy2: %v", m)
	}
	if m["ports"] != "443,8000-9000" {
		t.Errorf("ports = %v", m["ports"])
	}
	if m["obfs"] != "salamander" || m["obfs-password"] != "obfspw" {
		t.Errorf("obfs: %v", m)
	}
	if m["skip-cert-verify"] != true {
		t.Errorf("insecure flag lost")
	}
}

// --- tuic -----------------------------------------------------------------------

func TestParseTUIC(t *testing.T) {
	link := "tuic://uuid-xx:passyy@tu.example.com:8443?sni=tu.example.com&alpn=h3&congestion_control=bbr&udp_relay_mode=native#TU"
	n, err := parseShareLink(link)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, n)
	if m["uuid"] != "uuid-xx" || m["password"] != "passyy" {
		t.Fatalf("tuic auth: %v", m)
	}
	if m["congestion-controller"] != "bbr" || m["udp-relay-mode"] != "native" {
		t.Errorf("tuic opts: %v", m)
	}
}

// --- ssr ------------------------------------------------------------------------

func TestParseSSR(t *testing.T) {
	main := "1.2.3.4:8388:origin:aes-256-cfb:plain:" + b64std("ssrpass")
	params := "obfsparam=" + b64std("") + "&protoparam=" + b64std("32:aa") + "&remarks=" + b64std("SSR Node")
	link := "ssr://" + b64std(main+"/?"+params)
	n, err := parseShareLink(link)
	if err != nil {
		t.Fatal(err)
	}
	m := nodeMap(t, n)
	if m["name"] != "SSR Node" || m["protocol"] != "origin" || m["obfs"] != "plain" {
		t.Fatalf("ssr: %v", m)
	}
	if m["protocol-param"] != "32:aa" {
		t.Errorf("protocol-param = %v", m["protocol-param"])
	}
}

// --- content decoding ---------------------------------------------------------

func TestDecodeContentBase64LinkList(t *testing.T) {
	list := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@h:1#A\ntrojan://p@h:2#B\n"
	dec, err := DecodeContent(b64std(list))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Parsed.Kind != KindNodes || len(dec.Parsed.Nodes) != 2 {
		t.Fatalf("kind=%v nodes=%d", dec.Parsed.Kind, len(dec.Parsed.Nodes))
	}
	if dec.Parsed.Nodes[0].Name != "A" || dec.Parsed.Nodes[1].Name != "B" {
		t.Errorf("names: %+v", dec.Parsed.Nodes)
	}
}

func TestSanitizeDropsBrokenNodes(t *testing.T) {
	list := "vless://not-a-uuid@h:1#Broken\ntrojan://p@h:2#Good\n"
	dec, err := DecodeContent(b64std(list))
	if err != nil {
		t.Fatal(err)
	}
	if len(dec.Parsed.Nodes) != 1 || dec.Parsed.Nodes[0].Name != "Good" {
		t.Fatalf("sanitize should keep only valid nodes: %+v", dec.Parsed.Nodes)
	}
	if len(dec.Warnings) == 0 {
		t.Error("expected a warning for the dropped node")
	}
}

func TestDecodeContentClashWithGroups(t *testing.T) {
	y := `
port: 7890
proxies:
  - {name: N1, type: ss, server: 1.1.1.1, port: 80, cipher: aes-128-gcm, password: x}
proxy-groups:
  - name: PG
    type: select
    proxies: [N1]
rules:
  - MATCH,PG
`
	dec, err := DecodeContent(y)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Parsed.Kind != KindClash {
		t.Fatalf("kind = %v", dec.Parsed.Kind)
	}
	if !strings.Contains(dec.Parsed.Raw, "proxy-groups") {
		t.Error("raw clash content lost")
	}
}

func TestDecodeContentProxiesOnlyYAML(t *testing.T) {
	y := `
proxies:
  - {name: A, type: socks5, server: 1.1.1.1, port: 1080}
  - {name: B, type: socks5, server: 2.2.2.2, port: 1080}
`
	dec, err := DecodeContent(y)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Parsed.Kind != KindNodes || len(dec.Parsed.Nodes) != 2 {
		t.Fatalf("kind=%v nodes=%d", dec.Parsed.Kind, len(dec.Parsed.Nodes))
	}
	if dec.Parsed.Nodes[0].Name != "A" {
		t.Errorf("name = %v", dec.Parsed.Nodes[0].Name)
	}
}

func TestDecodeDedupesNames(t *testing.T) {
	list := "socks5://1.1.1.1:1080#Same\nsocks5://2.2.2.2:1080#Same\n"
	dec, err := DecodeContent(b64std(list))
	if err != nil {
		t.Fatal(err)
	}
	if dec.Parsed.Nodes[0].Name != "Same" || dec.Parsed.Nodes[1].Name != "Same #2" {
		t.Fatalf("dedupe: %q %q", dec.Parsed.Nodes[0].Name, dec.Parsed.Nodes[1].Name)
	}
}

func TestProviderRoundtrip(t *testing.T) {
	link := "trojan://pw@h.com:443?sni=h.com#RT"
	n, _ := parseShareLink(link)
	data, err := RenderProviderYAML([]Node{*n})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "type: trojan") {
		t.Fatalf("provider yaml: %s", data)
	}
}
