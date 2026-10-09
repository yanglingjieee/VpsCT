package subscription

import (
	"strings"
	"testing"

	"ctlvps/internal/domain"
)

// The line a Shadowrocket user taps on the home page is PROXY; a group of the
// profile's own is chosen elsewhere and would ignore that tap.
func TestShadowrocketNoRulesFollowsHomeSelection(t *testing.T) {
	b := &Bundle{Name: "demo", Proxies: sampleBundle().Proxies, Template: &domain.RuleTemplate{Kind: "shadowrocket", Content: NoRules("shadowrocket")}}
	r, err := RenderShadowrocket(b)
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	for _, want := range []string{"HK Reality = vless, hk.example.com, 443", "FINAL,PROXY"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
	for _, bad := range []string{"[Proxy Group]", "select", "{{"} {
		if strings.Contains(body, bad) {
			t.Fatalf("unexpected %q in:\n%s", bad, body)
		}
	}
}

func TestShadowrocketWithoutLinesRejects(t *testing.T) {
	tpl := `[Proxy]
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
	r, err := RenderShadowrocket(&Bundle{Name: "paused", Template: &domain.RuleTemplate{Kind: "shadowrocket", Content: tpl}})
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

	with, err := RenderShadowrocket(&Bundle{Name: "active", Proxies: sampleBundle().Proxies, Template: &domain.RuleTemplate{Kind: "shadowrocket", Content: tpl}})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(with.Body); !strings.Contains(got, "FINAL,PROXY") || strings.Contains(got, "REJECT") {
		t.Fatalf("a user with lines keeps PROXY:\n%s", got)
	}
}

// A comment that names a placeholder is left as written.
func TestConfCommentsKeepPlaceholders(t *testing.T) {
	comments := "# {{PROXIES}} 和 {{all}} 由面板填写\n; {{PROXY_GROUPS}} {{RULES}}\n"
	tpl := comments + `[Proxy]
{{PROXIES}}

[Rule]
FINAL,DIRECT
`
	for kind, render := range map[string]func(*Bundle) (*Rendered, error){"shadowrocket": RenderShadowrocket, "surge": RenderSurge} {
		r, err := render(&Bundle{Name: "demo", Proxies: sampleBundle().Proxies, Template: &domain.RuleTemplate{Kind: kind, Content: tpl}})
		if err != nil {
			t.Fatal(err)
		}
		body := string(r.Body)
		if head, _, _ := strings.Cut(body, "[Proxy]"); head != comments {
			t.Fatalf("%s: comments were filled in:\n%s", kind, body)
		}
		if strings.Count(body, "jp.example.com, 8443") != 1 {
			t.Fatalf("%s: {{PROXIES}} outside comments must be filled in once:\n%s", kind, body)
		}
		if strings.Contains(body, "\x00") {
			t.Fatalf("%s: marker left in the profile", kind)
		}
	}

	grouped := "# {{all}} 是全部线路\n[Proxy]\n{{PROXIES}}\n\n[Proxy Group]\n线路 = select,{{all|HK}}\n"
	r, err := RenderShadowrocket(&Bundle{Name: "demo", Proxies: sampleBundle().Proxies, Template: &domain.RuleTemplate{Kind: "shadowrocket", Content: grouped}})
	if err != nil {
		t.Fatal(err)
	}
	if body := string(r.Body); !strings.HasPrefix(body, "# {{all}} 是全部线路\n") || !strings.Contains(body, "线路 = select,HK Reality\n") {
		t.Fatalf("{{all}} is filled in outside comments only:\n%s", body)
	}
}
