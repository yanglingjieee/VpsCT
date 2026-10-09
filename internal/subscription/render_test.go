package subscription

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"ctlvps/internal/domain"
	"ctlvps/internal/proxynode"
)

func sampleBundle() *Bundle {
	proxies := []proxynode.Proxy{
		{Name: "HK Reality", Type: "vless", Server: "hk.example.com", Port: 443, Params: map[string]any{"uuid": "11111111-1111-1111-1111-111111111111", "flow": "xtls-rprx-vision", "tls": true, "servername": "www.apple.com", "client-fingerprint": "chrome", "reality-opts": map[string]any{"public-key": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "short-id": "ab"}}},
		{Name: "JP Hy2", Type: "hysteria2", Server: "jp.example.com", Port: 8443, Params: map[string]any{"password": "pw", "sni": "jp.example.com", "skip-cert-verify": true, "up": "50 Mbps", "down": "200 Mbps"}},
		{Name: "US Snell", Type: "snell", Server: "us.example.com", Port: 9000, Params: map[string]any{"psk": "psk", "version": 4}},
		{Name: "SG AnyTLS", Type: "anytls", Server: "sg.example.com", Port: 443, Params: map[string]any{"password": "pw", "sni": "sg.example.com"}},
		{Name: "TW SS", Type: "ss", Server: "tw.example.com", Port: 8388, Params: map[string]any{"cipher": "2022-blake3-aes-128-gcm", "password": "AAAAAAAAAAAAAAAAAAAAAA=="}},
	}
	exp := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	ui := &Userinfo{Upload: 1 << 30, Download: 5 << 30, Total: 100 << 30, Expire: &exp}
	return &Bundle{Name: "demo", Proxies: proxies, Userinfo: ui, InfoNodes: ui.InfoLines(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))}
}

func TestRenderRaw(t *testing.T) {
	b := sampleBundle()
	r, err := RenderRaw(b, false)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := base64.StdEncoding.DecodeString(string(r.Body))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(dec)), "\n")
	if len(lines) != 1+5 || !strings.HasPrefix(lines[0], "STATUS=") || !strings.HasPrefix(lines[1], "vless://") || !strings.HasPrefix(lines[3], "snell://") {
		t.Fatalf("raw lines: %v", lines)
	}
	plain, err := RenderRaw(b, true)
	if err != nil || !strings.Contains(string(plain.Body), "vless://") || plain.Format != FormatURIList {
		t.Fatalf("plain list: %v %s", err, plain.Body)
	}
}

// Lines of every type the panel can hand out appear in both profiles, and a
// Snell node of a version Mihomo does not speak is left out of its profile
// only.
func TestProfilesCarryEveryLine(t *testing.T) {
	b := sampleBundle()
	b.Proxies = append(b.Proxies, proxynode.Proxy{Name: "Old Snell", Type: "snell", Server: "old.example.com", Port: 9001, Params: map[string]any{"psk": "psk", "version": 6}})
	clash, err := RenderProfile(b, FormatMihomo, "MATCH,PROXY\n", "线路", "https://panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"HK Reality", "JP Hy2", "US Snell", "SG AnyTLS", "TW SS"} {
		if strings.Count(string(clash.Body), name) != 2 {
			t.Fatalf("%s must be a proxy and a member of the selector:\n%s", name, clash.Body)
		}
	}
	if strings.Contains(string(clash.Body), "Old Snell") || len(b.Proxies) != 6 {
		t.Fatalf("an unsupported Snell version is left out of the Clash profile, and only there:\n%s", clash.Body)
	}
	rocket, err := RenderProfile(b, FormatShadowrocket, "MATCH,PROXY\n", "线路", "https://panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"HK Reality = vless, hk.example.com, 443", "JP Hy2 = hysteria2, jp.example.com, 8443, password=pw", "US Snell = snell, us.example.com, 9000, password=psk, version=4",
		"SG AnyTLS = anytls, sg.example.com, 443, password=pw", "TW SS = ss, tw.example.com, 8388, encrypt-method=2022-blake3-aes-128-gcm", "Old Snell = snell, old.example.com, 9001",
	} {
		if !strings.Contains(string(rocket.Body), want) {
			t.Fatalf("shadowrocket lacks %q:\n%s", want, rocket.Body)
		}
	}
}

func TestDetectFormat(t *testing.T) {
	cases := map[string]string{
		"Shadowrocket/2.2.40 CFNetwork": FormatShadowrocket,
		"Surge iOS/3211":                "",
		"clash-verge/v2.0.0":            FormatMihomo,
		"ClashMetaForAndroid/2.11":      FormatMihomo,
		"sing-box 1.11.0":               "",
		"Hiddify/2.5.7":                 FormatRaw,
		"v2rayNG/1.9.0":                 FormatRaw,
		"v2rayU/4.2.8":                  FormatRaw,
		"V2RayX/1.0":                    FormatRaw,
		"Mozilla/5.0":                   "",
	}
	for ua, want := range cases {
		if got := DetectFormat(ua); got != want {
			t.Errorf("%s: got %s want %s", ua, got, want)
		}
	}
}

func TestShareUserinfoDual(t *testing.T) {
	sh := domain.Share{UsedUpload: 100, UsedDownload: 200, QuotaBytes: 1000, BillingMode: domain.BillingSum}
	ui := shareUserinfo(sh)
	if ui.Upload != 100 || ui.Download != 200 || ui.Remaining() != 700 {
		t.Fatalf("sum: %+v rem=%d", ui, ui.Remaining())
	}
	sh.BillingMode = domain.BillingDual
	ui = shareUserinfo(sh)
	if ui.Upload != 100 || ui.Download != 200 || ui.Remaining() != 700 {
		t.Fatalf("dual: %+v rem=%d", ui, ui.Remaining())
	}
}

func TestUserinfo(t *testing.T) {
	ui, ok := ParseUserinfo("upload=100; download=200; total=1000; expire=1893456000")
	if !ok || ui.Upload != 100 || ui.Download != 200 || ui.Total != 1000 || ui.Expire == nil {
		t.Fatalf("%+v %v", ui, ok)
	}
	if ui.Remaining() != 700 {
		t.Fatal("remaining")
	}
	if !strings.Contains(ui.Header(), "expire=1893456000") {
		t.Fatal(ui.Header())
	}
	// For a client that prints the header as it is: sizes and a date.
	until := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		ui   Userinfo
		want string
	}{
		{Userinfo{Upload: 2230234763, Download: 30280548909, Total: 429496729600}, "upload=2.08GB; download=28.2GB; total=400GB"},
		{Userinfo{Download: 1536, Expire: &until}, "upload=0B; download=1.5KB; until=2026-12-31"},
	} {
		if got := tc.ui.Readable(); got != tc.want {
			t.Fatalf("readable: got %q, want %q", got, tc.want)
		}
	}
}
