package subscription

import (
	"strings"
	"testing"

	"ctlvps/internal/proxynode"
)

func TestShadowrocketImportedSnell(t *testing.T) {
	for _, input := range []string{
		`Snell = snell, snell.example.com, 9000, psk=test-secret, version=4, obfs=http, obfs-host=example.com, reuse=true, tfo=true`,
		`proxies:
  - {name: Snell, type: snell, server: snell.example.com, port: 9000, psk: test-secret, version: 4, obfs-opts: {mode: http, host: example.com}, reuse: true, tfo: true, udp: true}`,
	} {
		parsed := proxynode.ParseAny(input)
		if len(parsed.Errors) != 0 || len(parsed.Proxies) != 1 {
			t.Fatalf("import failed: %+v", parsed)
		}
		p := parsed.Proxies[0]
		line := ShadowrocketProxyLine(p)
		for _, want := range []string{"Snell = snell, snell.example.com, 9000, password=test-secret, version=4", "obfs=http", "obfs-host=example.com", "udp=1"} {
			if !strings.Contains(line, want) {
				t.Errorf("missing %q in %s", want, line)
			}
		}
		if strings.Contains(line, "psk=") || p.Str("psk") != "test-secret" {
			t.Error("Shadowrocket uses its native password field and leaves the stored credential alone")
		}
	}
}

func TestShadowrocketSnellOptions(t *testing.T) {
	p := proxynode.Proxy{Name: "Snell", Type: "snell", Server: "2001:db8::1", Port: 443, Params: map[string]any{"psk": "test,secret", "version": 5, "reuse": true, "tfo": true}}
	line := ShadowrocketProxyLine(p)
	if !strings.Contains(line, `password="test,secret", version=5`) {
		t.Fatalf("password quoting or explicit version lost: %s", line)
	}
	if !strings.Contains(line, "reuse=true, tfo=true") || strings.Contains(line, "udp=1") {
		t.Fatalf("optional flags changed: %s", line)
	}
	delete(p.Params, "version")
	if line := ShadowrocketProxyLine(p); !strings.Contains(line, "version=4") || strings.Contains(line, "<nil>") {
		t.Fatalf("invalid default Snell options: %s", line)
	}
}
