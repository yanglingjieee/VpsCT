package subscription

import (
	"strings"
	"testing"
)

// In Shadowrocket PROXY is the node picked on the home page, which no profile
// defines: a user left without lines gets REJECT wherever a rule says PROXY,
// and nothing else in the profile is touched.
func TestShadowrocketWithoutLinesRejects(t *testing.T) {
	profile := `[Proxy]
{{PROXIES}}

[Rule]
# 国外走 PROXY
DOMAIN,panel.example.com,DIRECT
DOMAIN-SUFFIX,proxy.example,PROXY
RULE-SET,https://example.com/proxy.list, proxy
AND,((PROTOCOL,UDP),(DST-PORT,443)),PROXY
IP-CIDR,203.0.113.0/24,PROXY,no-resolve // PROXY, by address
FINAL,PROXY

[Host]
PROXY = 127.0.0.1
`
	r, err := RenderShadowrocket(&Bundle{Name: "paused"}, profile)
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	for _, want := range []string{
		"# 国外走 PROXY",
		"DOMAIN,panel.example.com,DIRECT",
		"DOMAIN-SUFFIX,proxy.example,REJECT",
		"RULE-SET,https://example.com/proxy.list,REJECT",
		"AND,((PROTOCOL,UDP),(DST-PORT,443)),REJECT",
		"IP-CIDR,203.0.113.0/24,REJECT,no-resolve // PROXY, by address",
		"FINAL,REJECT",
		"PROXY = 127.0.0.1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
	with, err := RenderShadowrocket(&Bundle{Name: "active", Proxies: sampleBundle().Proxies}, profile)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(with.Body); !strings.Contains(got, "FINAL,PROXY") || strings.Contains(got, "REJECT") || strings.Contains(got, "{{") {
		t.Fatalf("a user with lines keeps PROXY:\n%s", got)
	}
}
