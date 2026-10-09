package subscription

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"

	"ctlvps/internal/ruleset"
)

// mihomoProfile builds the Clash / Mihomo profile of a rule set: the built-in
// skeleton, one selector named group holding every line, a provider for each
// list in use, and the rules with PROXY pointing at the selector. The result
// is the template RenderMihomo fills with the user's lines.
func mihomoProfile(rules []ruleset.Rule, group, site string) (string, error) {
	if group = strings.TrimSpace(group); group == "" {
		group = ruleset.DefaultGroup
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(skeletonMihomo), &doc); err != nil {
		return "", err
	}
	root := doc.Content[0]
	scalar := func(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }
	seq := func(flow bool, items ...string) *yaml.Node {
		n := &yaml.Node{Kind: yaml.SequenceNode}
		if flow {
			n.Style = yaml.FlowStyle
		}
		for _, v := range items {
			n.Content = append(n.Content, scalar(v))
		}
		return n
	}
	pairs := func(flow bool, kv ...any) *yaml.Node {
		n := &yaml.Node{Kind: yaml.MappingNode}
		if flow {
			n.Style = yaml.FlowStyle
		}
		for i := 0; i < len(kv); i += 2 {
			var val *yaml.Node
			switch v := kv[i+1].(type) {
			case *yaml.Node:
				val = v
			case int:
				val = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprint(v)}
			default:
				val = scalar(fmt.Sprint(v))
			}
			n.Content = append(n.Content, scalar(kv[i].(string)), val)
		}
		return n
	}

	// Names that go straight out are looked up at home, so that they resolve
	// to the addresses meant for a visitor from there.
	domestic := []string{"https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"}
	var homeLists, homeNames []string
	providers := &yaml.Node{Kind: yaml.MappingNode}
	seen := map[string]bool{}
	ruleSeq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, r := range rules {
		action := r.Action
		if action == ruleset.ActionProxy {
			action = group
		}
		parts := []string{r.Type, r.Value, action}
		switch r.Type {
		case "MATCH":
			parts = []string{"MATCH", action}
		case "RULE-SET":
			l, _ := ruleset.ListNamed(r.Value)
			if !seen[l.Name] {
				seen[l.Name] = true
				providers.Content = append(providers.Content, scalar(l.Name), pairs(true,
					"type", "http", "behavior", l.Kind, "format", "yaml",
					"url", site+"/rules/clash/"+l.Name+".yaml", "path", "./rules/"+l.Name+".yaml", "interval", 86400))
				if l.Kind == "domain" && r.Action == ruleset.ActionDirect {
					homeLists = append(homeLists, l.Name)
				}
			}
		case "DOMAIN":
			if r.Action == ruleset.ActionDirect {
				homeNames = append(homeNames, r.Value)
			}
		case "DOMAIN-SUFFIX":
			if r.Action == ruleset.ActionDirect {
				homeNames = append(homeNames, "+."+r.Value)
			}
		}
		if r.NoResolve {
			parts = append(parts, "no-resolve")
		}
		ruleSeq.Content = append(ruleSeq.Content, scalar(strings.Join(parts, ",")))
	}

	dns := getMapKey(root, "dns")
	setMapKey(dns, "nameserver", seq(false, "https://1.1.1.1/dns-query#"+group, "https://8.8.8.8/dns-query#"+group))
	policy := &yaml.Node{Kind: yaml.MappingNode}
	if len(homeLists) > 0 {
		policy.Content = append(policy.Content, scalar("rule-set:"+strings.Join(homeLists, ",")), seq(true, domestic...))
	}
	for _, name := range homeNames {
		policy.Content = append(policy.Content, scalar(name), seq(true, domestic...))
	}
	if len(policy.Content) > 0 {
		setMapKey(dns, "nameserver-policy", policy)
	} else {
		deleteMapKey(dns, "nameserver-policy")
	}

	groups := &yaml.Node{Kind: yaml.SequenceNode}
	groups.Content = append(groups.Content, pairs(false, "name", group, "type", "select", "proxies", seq(false, "{{all}}")))
	setMapKey(root, "proxy-groups", groups)
	if len(providers.Content) > 0 {
		setMapKey(root, "rule-providers", providers)
	}
	setMapKey(root, "rules", ruleSeq)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", err
	}
	enc.Close()
	return buf.String(), nil
}

// shadowrocketProfile builds the Shadowrocket profile of a rule set. The app
// lists the profile's lines on its home page and PROXY is the one tapped
// there, so the rules name PROXY as they are and no group is declared: a
// group is chosen on another screen and would ignore that tap.
func shadowrocketProfile(rules []ruleset.Rule, site string) string {
	var b strings.Builder
	b.WriteString(skeletonShadowrocket)
	b.WriteString("\n[Proxy]\n{{PROXIES}}\n\n[Rule]\n")
	for _, r := range rules {
		parts := []string{r.Type, r.Value, r.Action}
		switch r.Type {
		case "MATCH":
			parts = []string{"FINAL", r.Action}
		case "RULE-SET":
			l, _ := ruleset.ListNamed(r.Value)
			if l.Kind == "classical" {
				continue
			}
			// The list's own lines say no-resolve where it applies.
			b.WriteString("RULE-SET," + site + "/rules/surge/" + l.Name + ".list," + r.Action + "\n")
			continue
		}
		if r.NoResolve {
			parts = append(parts, "no-resolve")
		}
		b.WriteString(strings.Join(parts, ",") + "\n")
	}
	b.WriteString("\n[Host]\nlocalhost = 127.0.0.1\n")
	return b.String()
}

// Profile returns the template of a rule set for a client family ("mihomo" or
// "shadowrocket"), ready for the renderer to fill with a user's lines. site
// is the panel's public address, where the lists are served.
func Profile(kind, rules, group, site string) (string, error) {
	parsed, err := ruleset.Parse(rules)
	if err != nil {
		return "", err
	}
	if u, err := url.Parse(site); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("面板的对外地址无效：%q", site)
	}
	site = strings.TrimRight(site, "/")
	if kind == "shadowrocket" {
		return shadowrocketProfile(parsed, site), nil
	}
	return mihomoProfile(parsed, group, site)
}
