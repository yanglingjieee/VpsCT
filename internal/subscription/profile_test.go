package subscription

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const sampleRules = `# 白名单：国内直连，其余走所选线路
DOMAIN,panel.example.com,DIRECT
DOMAIN-SUFFIX,corp.example,DIRECT
DOMAIN,mask.icloud.com,REJECT
IP-CIDR6,2000::/3,REJECT,no-resolve
RULE-SET,applications,DIRECT
RULE-SET,direct,DIRECT
RULE-SET,proxy,PROXY
RULE-SET,cncidr,DIRECT
RULE-SET,telegramcidr,PROXY,no-resolve
MATCH,PROXY
`

// One list of rules, two clients: Clash gets a selector of lines that PROXY
// points at and a provider per list; Shadowrocket gets PROXY itself, which is
// the line tapped on its home page, and no group to choose in elsewhere.
func TestOneRuleSetForEveryClient(t *testing.T) {
	site := "https://panel.example.com"
	render := func(kind string, b *Bundle) string {
		t.Helper()
		r, err := RenderProfile(b, kind, sampleRules, "🥔 我的线路", site+"/")
		if err != nil {
			t.Fatal(err)
		}
		return string(r.Body)
	}
	lines := sampleBundle().Proxies[:2]

	var clash struct {
		Proxies []map[string]any `yaml:"proxies"`
		Groups  []struct {
			Name    string   `yaml:"name"`
			Type    string   `yaml:"type"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
		Providers map[string]map[string]any `yaml:"rule-providers"`
		Rules     []string                  `yaml:"rules"`
		DNS       struct {
			Nameserver []string            `yaml:"nameserver"`
			Policy     map[string][]string `yaml:"nameserver-policy"`
		} `yaml:"dns"`
	}
	body := render("mihomo", &Bundle{Name: "demo", Proxies: lines})
	if err := yaml.Unmarshal([]byte(body), &clash); err != nil {
		t.Fatalf("%v\n%s", err, body)
	}
	if len(clash.Proxies) != 2 || len(clash.Groups) != 1 || clash.Groups[0].Name != "🥔 我的线路" || clash.Groups[0].Type != "select" || strings.Join(clash.Groups[0].Proxies, "|") != "HK Reality|JP Hy2" {
		t.Fatalf("lines and their selector: %+v %+v", clash.Proxies, clash.Groups)
	}
	wantRules := []string{
		"DOMAIN,panel.example.com,DIRECT", "DOMAIN-SUFFIX,corp.example,DIRECT", "DOMAIN,mask.icloud.com,REJECT", "IP-CIDR6,2000::/3,REJECT,no-resolve",
		"RULE-SET,applications,DIRECT", "RULE-SET,direct,DIRECT", "RULE-SET,proxy,🥔 我的线路", "RULE-SET,cncidr,DIRECT", "RULE-SET,telegramcidr,🥔 我的线路,no-resolve", "MATCH,🥔 我的线路",
	}
	if strings.Join(clash.Rules, "\n") != strings.Join(wantRules, "\n") {
		t.Fatalf("clash rules:\n%s", strings.Join(clash.Rules, "\n"))
	}
	if len(clash.Providers) != 5 || clash.Providers["cncidr"]["behavior"] != "ipcidr" || clash.Providers["applications"]["behavior"] != "classical" ||
		clash.Providers["proxy"]["url"] != site+"/rules/clash/proxy.yaml" || clash.Providers["proxy"]["interval"] != 86400 {
		t.Fatalf("a provider for each list in use, fetched from the panel: %v", clash.Providers)
	}
	// Foreign names are resolved through the chosen line, names that go
	// straight out at home.
	if clash.DNS.Nameserver[0] != "https://1.1.1.1/dns-query#🥔 我的线路" || len(clash.DNS.Policy) != 3 ||
		clash.DNS.Policy["rule-set:direct"] == nil || clash.DNS.Policy["panel.example.com"] == nil || clash.DNS.Policy["+.corp.example"] == nil {
		t.Fatalf("dns: %+v", clash.DNS)
	}

	rocket := render("shadowrocket", &Bundle{Name: "demo", Proxies: lines})
	_, rules, _ := strings.Cut(rocket, "[Rule]\n")
	rules, _, _ = strings.Cut(rules, "\n\n")
	want := `DOMAIN,panel.example.com,DIRECT
DOMAIN-SUFFIX,corp.example,DIRECT
DOMAIN,mask.icloud.com,REJECT
IP-CIDR6,2000::/3,REJECT,no-resolve
RULE-SET,https://panel.example.com/rules/surge/direct.list,DIRECT
RULE-SET,https://panel.example.com/rules/surge/proxy.list,PROXY
RULE-SET,https://panel.example.com/rules/surge/cncidr.list,DIRECT
RULE-SET,https://panel.example.com/rules/surge/telegramcidr.list,PROXY
FINAL,PROXY`
	if rules != want {
		t.Fatalf("shadowrocket rules:\n%s", rules)
	}
	if !strings.Contains(rocket, "[General]\nbypass-system = true") || !strings.Contains(rocket, "HK Reality = vless, hk.example.com, 443") || strings.Contains(rocket, "[Proxy Group]") || strings.Contains(rocket, "我的线路") || strings.Contains(rocket, "{{") {
		t.Fatalf("shadowrocket profile:\n%s", rocket)
	}

	// A user without lines stops working in both; nothing turns direct.
	if none := render("mihomo", &Bundle{Name: "paused"}); !strings.Contains(none, "- REJECT") || !strings.Contains(none, "MATCH,") {
		t.Fatalf("clash without lines:\n%s", none)
	}
	if none := render("shadowrocket", &Bundle{Name: "paused"}); !strings.Contains(none, "FINAL,REJECT") || strings.Contains(none, ",PROXY") || !strings.Contains(none, "DOMAIN,panel.example.com,DIRECT") {
		t.Fatalf("shadowrocket without lines:\n%s", none)
	}

	if _, err := Profile("mihomo", "DOMAIN,a.example\n", "", site); err == nil {
		t.Fatal("rules that do not read must not become a profile")
	}
	if _, err := Profile("mihomo", sampleRules, "", "not a url"); err == nil {
		t.Fatal("lists need the panel's address")
	}
}
