package profile

// uriexport_test.go — round-trip proof for share-link export (#102):
// every parser in uriparse.go has an exporter in uriexport.go, and
// parse(export(parse(link))) must preserve the semantic core (server,
// port, credentials, transport, TLS) AND export() must be idempotent
// (export(parse(export(parse(link)))) is byte-stable), which proves the
// canonical form round-trips indefinitely.

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"reflect"
	"strings"
	"testing"
)

// b64urlRaw encodes with the URL-safe unpadded alphabet (SIP002 / SSR).
func b64urlRaw(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// decodeDataURLImage decodes a data:image/png;base64,... URL back into an
// image (the QR round-trip helper).
func decodeDataURLImage(t *testing.T, dataURL string) (image.Image, error) {
	t.Helper()
	payload, ok := strings.CutPrefix(dataURL, "data:image/png;base64,")
	if !ok {
		t.Fatalf("bad data url")
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, err
	}
	return png.Decode(bytes.NewReader(raw))
}

// roundTrip parses a sample link, exports it, re-parses the export and
// returns both parsed maps.
func roundTrip(t *testing.T, sample string) (map[string]any, map[string]any) {
	t.Helper()
	n1, err := parseShareLink(sample)
	if err != nil {
		t.Fatalf("parse sample: %v", err)
	}
	info, err := ExportShareInfo(normMap(n1.Raw))
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if info.Kind != ShareKindURI {
		t.Fatalf("kind = %q, want uri", info.Kind)
	}
	n2, err := parseShareLink(info.Text)
	if err != nil {
		t.Fatalf("re-parse exported link %q: %v", info.Text, err)
	}
	// Idempotence: exporting the re-parsed node must give the same string.
	info2, err := ExportShareInfo(normMap(n2.Raw))
	if err != nil {
		t.Fatalf("second export: %v", err)
	}
	if info2.Text != info.Text {
		t.Errorf("export not idempotent:\nfirst : %s\nsecond: %s", info.Text, info2.Text)
	}
	return nodeMap(t, n1), nodeMap(t, n2)
}

// mustKeep asserts that the semantic core survived the round trip.
func mustKeep(t *testing.T, m1, m2 map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if k == "ws-opts" || k == "grpc-opts" || k == "h2-opts" ||
			k == "http-opts" || k == "httpupgrade-opts" || k == "splithttp-opts" {
			continue // compared separately per transport
		}
		if m1[k] != m2[k] {
			t.Errorf("%s: got %v, want %v", k, m2[k], m1[k])
		}
	}
}

func TestExportRoundTripVMessWS(t *testing.T) {
	json := `{"v":"2","ps":"Tokyo Node","add":"1.2.3.4","port":"443","id":"aaaabbbb-cccc-dddd-eeee-ffff00001111","aid":"0","scy":"auto","net":"ws","type":"none","host":"cdn.example.com","path":"/ws","tls":"tls","sni":"cdn.example.com","alpn":"h2,http/1.1","fp":"chrome"}`
	m1, m2 := roundTrip(t, "vmess://"+b64std(json))
	mustKeep(t, m1, m2, "name", "type", "server", "port", "uuid", "cipher", "tls", "servername", "network", "client-fingerprint")
	ws1 := m1["ws-opts"].(map[string]any)
	ws2 := m2["ws-opts"].(map[string]any)
	if ws1["path"] != ws2["path"] {
		t.Errorf("ws path: got %v, want %v", ws2["path"], ws1["path"])
	}
	if ws1["headers"].(map[string]any)["Host"] != ws2["headers"].(map[string]any)["Host"] {
		t.Errorf("ws Host header lost")
	}
}

func TestExportRoundTripVMessHTTPObfs(t *testing.T) {
	// v2rayN encodes TCP + HTTP obfs as net=tcp & type=http (#102).
	json := `{"v":"2","ps":"h-obfs","add":"9.9.9.9","port":8080,"id":"u-1","aid":0,"scy":"auto","net":"tcp","type":"http","host":"a.b.c","path":"/p"}`
	m1, m2 := roundTrip(t, "vmess://"+b64std(json))
	mustKeep(t, m1, m2, "name", "server", "port", "uuid", "network")
	if m1["network"] != "http" {
		t.Fatalf("parse should map net=tcp+type=http to network http, got %v", m1["network"])
	}
	ho1 := m1["http-opts"].(map[string]any)
	ho2 := m2["http-opts"].(map[string]any)
	if ho1["path"].([]any)[0] != ho2["path"].([]any)[0] {
		t.Errorf("http-opts path lost: %v vs %v", ho2["path"], ho1["path"])
	}
}

func TestExportRoundTripVLESSReality(t *testing.T) {
	sample := "vless://uuid-1234@5.6.7.8:8443?encryption=none&security=reality&sni=www.microsoft.com&fp=chrome&pbk=PUBKEY123&sid=ab12&flow=xtls-rprx-vision&type=tcp#Reality%20Node"
	m1, m2 := roundTrip(t, sample)
	mustKeep(t, m1, m2, "name", "type", "server", "port", "uuid", "flow", "tls", "servername", "client-fingerprint")
	re1 := m1["reality-opts"].(map[string]any)
	re2 := m2["reality-opts"].(map[string]any)
	if re1["public-key"] != re2["public-key"] || re1["short-id"] != re2["short-id"] {
		t.Errorf("reality opts lost: %v vs %v", re2, re1)
	}
}

func TestExportRoundTripVLESSWs(t *testing.T) {
	sample := "vless://u2@[2001:db8::1]:2053?security=tls&sni=s.example&fp=firefox&type=ws&host=cdn.example&path=%2Fw&allowInsecure=1#v6-ws"
	m1, m2 := roundTrip(t, sample)
	mustKeep(t, m1, m2, "name", "server", "port", "uuid", "tls", "servername", "network", "client-fingerprint", "skip-cert-verify")
	ws1 := m1["ws-opts"].(map[string]any)
	ws2 := m2["ws-opts"].(map[string]any)
	if ws1["path"] != ws2["path"] || ws1["headers"].(map[string]any)["Host"] != ws2["headers"].(map[string]any)["Host"] {
		t.Errorf("ws opts: %v vs %v", ws2, ws1)
	}
}

func TestExportRoundTripVLESSPQEncryption(t *testing.T) {
	sample := "vless://u3@1.1.1.1:443?encryption=mlkem768x25519plus&security=tls&sni=x.example&type=grpc&serviceName=gs#pq"
	m1, m2 := roundTrip(t, sample)
	mustKeep(t, m1, m2, "uuid", "encryption", "servername", "network")
	g1 := m1["grpc-opts"].(map[string]any)
	g2 := m2["grpc-opts"].(map[string]any)
	if g1["grpc-service-name"] != g2["grpc-service-name"] {
		t.Errorf("grpc service name lost: %v vs %v", g2, g1)
	}
}

func TestExportRoundTripTrojan(t *testing.T) {
	sample := "trojan://pw123@3.4.5.6:443?security=tls&sni=t.example&alpn=h2,http/1.1&fp=safari&allowInsecure=1&type=ws&host=h.example&path=%2Ft#Trojan%20N"
	m1, m2 := roundTrip(t, sample)
	mustKeep(t, m1, m2, "name", "type", "server", "port", "password", "tls", "sni", "network", "client-fingerprint", "skip-cert-verify")
	ws1 := m1["ws-opts"].(map[string]any)
	ws2 := m2["ws-opts"].(map[string]any)
	if ws1["path"] != ws2["path"] {
		t.Errorf("trojan ws path lost")
	}
}

func TestExportRoundTripTrojanReality(t *testing.T) {
	sample := "trojan://tpw@7.7.7.7:443?security=reality&pbk=PB1&sid=cd34&sni=tr.example&fp=chrome&allowInsecure=1#tr-r"
	m1, m2 := roundTrip(t, sample)
	re1 := m1["reality-opts"].(map[string]any)
	re2 := m2["reality-opts"].(map[string]any)
	if re1["public-key"] != re2["public-key"] {
		t.Errorf("trojan reality pbk lost")
	}
}

func TestExportRoundTripSSObfs(t *testing.T) {
	sample := "ss://" + b64urlRaw("aes-128-gcm:passw0rd") + "@2.3.4.5:8388?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dcdn.x.io#ss-obfs"
	m1, m2 := roundTrip(t, sample)
	mustKeep(t, m1, m2, "name", "type", "server", "port", "cipher", "password", "plugin")
	po1 := m1["plugin-opts"].(map[string]any)
	po2 := m2["plugin-opts"].(map[string]any)
	if po1["mode"] != po2["mode"] || po1["host"] != po2["host"] {
		t.Errorf("plugin opts: %v vs %v", po2, po1)
	}
}

func TestExportRoundTripSSV2rayPlugin(t *testing.T) {
	sample := "ss://" + b64urlRaw("chacha20-ietf-poly1305:pass2") + "@6.6.6.6:1234?plugin=v2ray-plugin%3Bmode%3Dwebsocket%3Btls%3Bhost%3Dv.example%3Bpath%3D%2Fv#ss-vp"
	m1, m2 := roundTrip(t, sample)
	mustKeep(t, m1, m2, "cipher", "password", "plugin")
	po1 := m1["plugin-opts"].(map[string]any)
	po2 := m2["plugin-opts"].(map[string]any)
	if po1["tls"] != true || po1["host"] != po2["host"] || po1["path"] != po2["path"] {
		t.Errorf("v2ray-plugin opts: %v vs %v", po2, po1)
	}
}

func TestExportRoundTripSSR(t *testing.T) {
	main := "1.2.3.4:9999:auth_aes128_md5:aes-256-cfb:tls1.2_ticket_auth:" + b64urlRaw("ssrpass")
	sample := "ssr://" + b64urlRaw(main+"/?remarks="+b64urlRaw("SSR N")+"&obfsparam="+b64urlRaw("obparam")+"&protoparam="+b64urlRaw("32"))
	m1, m2 := roundTrip(t, sample)
	mustKeep(t, m1, m2, "name", "type", "server", "port", "cipher", "password", "protocol", "obfs", "protocol-param", "obfs-param")
}

func TestExportRoundTripHysteria2(t *testing.T) {
	sample := "hysteria2://hyauth@8.8.4.4:36712?sni=h.example&insecure=1&obfs=salamander&obfs-password=obpw&mport=443,8000-9000&pinSHA256=deadbeef#hy2-n"
	m1, m2 := roundTrip(t, sample)
	mustKeep(t, m1, m2, "name", "type", "server", "port", "password", "sni", "skip-cert-verify", "obfs", "obfs-password", "ports", "fingerprint")
}

func TestExportRoundTripTUICv5(t *testing.T) {
	sample := "tuic://uuid-x:tuicpw@4.4.4.4:443?congestion_control=bbr&udp_relay_mode=native&sni=tu.example&alpn=h3&allow_insecure=1#tuic5"
	m1, m2 := roundTrip(t, sample)
	mustKeep(t, m1, m2, "name", "type", "server", "port", "uuid", "password", "congestion-controller", "udp-relay-mode", "sni", "skip-cert-verify")
}

func TestExportRoundTripTUICv4Token(t *testing.T) {
	sample := "tuic://token-xyz@5.5.5.5:443?congestion_control=cubic&sni=tv.example#tuic4"
	m1, m2 := roundTrip(t, sample)
	mustKeep(t, m1, m2, "token", "congestion-controller", "sni")
}

func TestExportRoundTripAnyTLS(t *testing.T) {
	sample := "anytls://atpw@9.8.7.6:8443?sni=at.example&insecure=1&fp=chrome&alpn=h2#anytls-n"
	m1, m2 := roundTrip(t, sample)
	mustKeep(t, m1, m2, "name", "type", "server", "port", "password", "sni", "skip-cert-verify", "client-fingerprint")
}

func TestExportRoundTripSocks(t *testing.T) {
	sample := "socks5://user:pw@10.0.0.2:1080#socksn"
	m1, m2 := roundTrip(t, sample)
	mustKeep(t, m1, m2, "name", "type", "server", "port", "username", "password")
}

func TestExportRoundTripSocksTLS(t *testing.T) {
	sample := "socks5s://10.0.0.3:1080#sockstls"
	_, m2 := roundTrip(t, sample)
	if m2["tls"] != true {
		t.Errorf("socks tls lost")
	}
}

func TestExportRoundTripHTTP(t *testing.T) {
	sample := "https://u1:p1@10.0.0.4:3128#httpsn"
	m1, m2 := roundTrip(t, sample)
	mustKeep(t, m1, m2, "name", "type", "server", "port", "username", "password", "tls")
}

// --- WireGuard ---------------------------------------------------------------

func TestExportRoundTripWireGuard(t *testing.T) {
	conf := "[Interface]\nAddress = 10.10.0.2/32, fd00::2/128\nPrivateKey = " + strings.Repeat("A", 43) + "=\nDNS = 1.1.1.1, 8.8.8.8\nMTU = 1420\n\n[Peer]\nPublicKey = " + strings.Repeat("B", 43) + "=\nEndpoint = wg.example.com:51820\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 25\n"
	n, err := decodeWGQuick(conf)
	if err != nil {
		t.Fatalf("parse wg conf: %v", err)
	}
	node := n.Parsed.Nodes[0]
	info, err := ExportShareInfo(normMap(node.Raw))
	if err != nil {
		t.Fatalf("export wg: %v", err)
	}
	if info.Kind != ShareKindWGQuick {
		t.Fatalf("kind = %q", info.Kind)
	}
	// Re-import the exported conf and compare the core fields.
	d2, err := decodeWGQuick(info.Text)
	if err != nil {
		t.Fatalf("re-parse exported wg conf:\n%s\nerr: %v", info.Text, err)
	}
	m1 := nodeMap(t, &Node{Name: node.Name, Raw: node.Raw})
	m2 := nodeMap(t, &d2.Parsed.Nodes[0])
	for _, k := range []string{"type", "ip", "ipv6", "private-key", "dns", "mtu", "public-key", "allowed-ips", "persistent-keepalive", "server", "port"} {
		if !reflect.DeepEqual(m1[k], m2[k]) {
			t.Errorf("%s: got %v, want %v", k, m2[k], m1[k])
		}
	}
}

// --- QR ----------------------------------------------------------------------

func TestQRDataURLRoundTrip(t *testing.T) {
	link := "vless://uuid-1234@5.6.7.8:8443?security=tls&sni=x.example#n"
	dataURL := QRDataURL(link)
	if !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Fatalf("data url prefix wrong: %q", dataURL[:40])
	}
	// Decode the PNG and read the QR back out of it.
	img, err := decodeDataURLImage(t, dataURL)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	got, err := DecodeQR(img)
	if err != nil {
		t.Fatalf("decode qr: %v", err)
	}
	if got != link {
		t.Errorf("qr round trip: got %q, want %q", got, link)
	}
}

func TestQRDataURLEmpty(t *testing.T) {
	if QRDataURL("") != "" {
		t.Errorf("empty text must give empty data url")
	}
	if QRDataURL(strings.Repeat("x", 10000)) != "" {
		t.Errorf("oversized text must give empty data url")
	}
}

func TestIsImageFile(t *testing.T) {
	if !IsImageFile([]byte("\x89PNG\r\n\x1a\nrest")) {
		t.Errorf("png magic not detected")
	}
	if !IsImageFile([]byte{0xFF, 0xD8, 0xFF, 0xE0}) {
		t.Errorf("jpeg magic not detected")
	}
	if IsImageFile([]byte("proxies:\n  - name: x")) {
		t.Errorf("yaml misdetected as image")
	}
}
