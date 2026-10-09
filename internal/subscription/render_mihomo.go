package subscription

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Rendered is a profile ready to be served.
type Rendered struct {
	Body        []byte
	ContentType string
	Filename    string
	Format      string
}

var allMarker = regexp.MustCompile(`^\{\{\s*all(?:\|(.*?))?\s*\}\}$`)

// RenderMihomo fills a Clash / Mihomo profile (see mihomoProfile) with a
// user's lines: the proxies, and every {{all}} in a selector.
func RenderMihomo(b *Bundle, profile string) (*Rendered, error) {
	b = mihomoCompatibleBundle(b)
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(profile), &doc); err != nil {
		return nil, fmt.Errorf("配置解析失败: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("配置不是一个 YAML 映射")
	}
	root := doc.Content[0]
	// No pseudo node for the remaining traffic: clients read
	// Subscription-Userinfo, and a 127.0.0.1 first proxy makes refresh fail.
	proxyNodes := &yaml.Node{Kind: yaml.SequenceNode}
	for _, p := range b.Proxies {
		proxyNodes.Content = append(proxyNodes.Content, proxyMapNode(p.ClashMap()))
	}
	setMapKey(root, "proxies", proxyNodes)
	if groups := getMapKey(root, "proxy-groups"); groups != nil && groups.Kind == yaml.SequenceNode {
		expandGroupMarkers(groups, b.AllProxyNames())
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	enc.Close()
	return &Rendered{Body: buf.Bytes(), ContentType: "text/yaml; charset=utf-8", Filename: b.Name + ".yaml", Format: FormatMihomo}, nil
}

// mihomoCompatibleBundle leaves out what Mihomo cannot load: a Snell node of
// a version it does not speak, and any node dialed through one left out.
// Only the rendered copy is filtered.
func mihomoCompatibleBundle(b *Bundle) *Bundle {
	omitted := map[string]bool{}
	for _, p := range b.Proxies {
		if p.Type == "snell" && (p.Int("version") < 0 || p.Int("version") > 5) {
			omitted[p.Name] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, p := range b.Proxies {
			if !omitted[p.Name] && omitted[p.Str("dialer-proxy")] {
				omitted[p.Name] = true
				changed = true
			}
		}
	}
	if len(omitted) == 0 {
		return b
	}
	out := *b
	out.Proxies = nil
	for _, p := range b.Proxies {
		if !omitted[p.Name] {
			out.Proxies = append(out.Proxies, p)
		}
	}
	return &out
}

func expandGroupMarkers(groups *yaml.Node, allNames []string) {
	for _, g := range groups.Content {
		if g.Kind != yaml.MappingNode {
			continue
		}
		list := getMapKey(g, "proxies")
		if list == nil || list.Kind != yaml.SequenceNode {
			continue
		}
		var expanded []*yaml.Node
		hasMarker := false
		for _, item := range list.Content {
			if item.Kind != yaml.ScalarNode {
				expanded = append(expanded, item)
				continue
			}
			m := allMarker.FindStringSubmatch(strings.TrimSpace(item.Value))
			if m == nil {
				expanded = append(expanded, item)
				continue
			}
			hasMarker = true
			var re *regexp.Regexp
			if len(m) > 1 && m[1] != "" {
				re, _ = regexp.Compile(m[1])
			}
			for _, n := range allNames {
				if re != nil && !re.MatchString(n) {
					continue
				}
				expanded = append(expanded, &yaml.Node{Kind: yaml.ScalarNode, Value: n})
			}
		}
		if len(expanded) == 0 {
			if hasMarker && len(allNames) > 0 {
				for _, name := range allNames {
					expanded = append(expanded, &yaml.Node{Kind: yaml.ScalarNode, Value: name})
				}
			} else {
				expanded = []*yaml.Node{{Kind: yaml.ScalarNode, Value: EmptyGroupPolicy}}
			}
		}
		list.Content = expanded
	}
}

var proxyKeyOrder = []string{"name", "type", "server", "port", "ports"}

func proxyMapNode(m map[string]any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
	emitted := map[string]bool{}
	add := func(k string) {
		v, ok := m[k]
		if !ok || emitted[k] {
			return
		}
		emitted[k] = true
		var vn yaml.Node
		if err := vn.Encode(v); err != nil {
			return
		}
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &vn)
	}
	for _, k := range proxyKeyOrder {
		add(k)
	}
	rest := make([]string, 0, len(m))
	for k := range m {
		if !emitted[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		add(k)
	}
	return n
}

func getMapKey(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func deleteMapKey(m *yaml.Node, key string) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

func setMapKey(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, val)
}
