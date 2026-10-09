package ruleset

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	rules, err := Parse(`# 国内直连，其余走所选线路
DOMAIN,panel.example.com,DIRECT
domain-suffix, cn , direct
IP-CIDR,10.0.0.0/8,DIRECT,no-resolve
IP-CIDR6,2000::/3,REJECT,no-resolve
GEOIP,CN,DIRECT
RULE-SET,cncidr,DIRECT,no-resolve
RULE-SET,proxy,PROXY

FINAL,PROXY
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 8 || rules[1] != (Rule{Type: "DOMAIN-SUFFIX", Value: "cn", Action: ActionDirect}) || !rules[2].NoResolve || rules[7] != (Rule{Type: "MATCH", Action: ActionProxy}) {
		t.Fatalf("rules: %+v", rules)
	}
	if rules, _ = Parse("DOMAIN,a.example,REJECT\n"); len(rules) != 2 || rules[1].Type != "MATCH" || rules[1].Action != ActionProxy {
		t.Fatalf("a set without MATCH ends in MATCH,PROXY: %+v", rules)
	}
	if rules, err = Parse(Default); err != nil || len(rules) != 8 {
		t.Fatalf("the built-in default must read: %v %+v", err, rules)
	}
	for name, bad := range map[string]string{
		"no action":              "DOMAIN,a.example",
		"a group as action":      "DOMAIN,a.example,节点选择",
		"unknown type":           "PROCESS-NAME,aria2c,DIRECT",
		"unknown list":           "RULE-SET,nonesuch,DIRECT",
		"v6 in IP-CIDR":          "IP-CIDR,fc00::/7,DIRECT",
		"not a prefix":           "IP-CIDR,10.0.0.1,DIRECT",
		"no-resolve on a domain": "DOMAIN,a.example,DIRECT,no-resolve",
		"no-resolve on names":    "RULE-SET,proxy,PROXY,no-resolve",
		"rule after MATCH":       "MATCH,PROXY\nDOMAIN,a.example,DIRECT",
		"MATCH with a value":     "MATCH,a,PROXY",
		"stray option":           "GEOIP,CN,DIRECT,whatever",
	} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("%s: accepted", name)
		} else if name == "unknown list" && !strings.Contains(err.Error(), "cncidr") {
			t.Fatalf("an unknown list should name the ones there are: %v", err)
		}
	}
}

// A rule set written as a Clash profile becomes the one list: groups of lines
// turn into PROXY, and what the list cannot express is left out, not kept to
// break every user's next refresh.
func TestFromClashProfile(t *testing.T) {
	rules, group := FromClashProfile(`mixed-port: 7890
proxy-groups:
  - name: 🥔 土豆饼的家
    type: select
    proxies: ["{{all}}"]
  - name: 自动
    type: url-test
rule-providers:
  proxy: {type: http, behavior: domain}
rules:
  - DOMAIN,panel.example.com,DIRECT
  - DOMAIN,mask.icloud.com,REJECT-DROP
  - IP-CIDR6,2000::/3,REJECT,no-resolve
  - PROCESS-NAME,aria2c,DIRECT
  - RULE-SET,my-own-list,DIRECT
  - RULE-SET,proxy,🥔 土豆饼的家
  - RULE-SET,telegramcidr,自动,no-resolve
  - GEOSITE,cn,DIRECT
  - MATCH,🥔 土豆饼的家
  - DOMAIN,never.example,DIRECT
`)
	want := `DOMAIN,panel.example.com,DIRECT
DOMAIN,mask.icloud.com,REJECT
IP-CIDR6,2000::/3,REJECT,no-resolve
RULE-SET,proxy,PROXY
RULE-SET,telegramcidr,PROXY,no-resolve
MATCH,PROXY
`
	if rules != want || group != "🥔 土豆饼的家" {
		t.Fatalf("group %q, rules:\n%s", group, rules)
	}
	if _, err := Parse(rules); err != nil {
		t.Fatal(err)
	}
	if rules, group = FromClashProfile("proxies: [\n"); rules != "" || group != "" {
		t.Fatalf("an unreadable profile yields nothing: %q %q", rules, group)
	}
}
