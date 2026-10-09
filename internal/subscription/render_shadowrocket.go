package subscription

import (
	"fmt"
	"strconv"
	"strings"

	"ctlvps/internal/proxynode"
)

func nonempty(v ...string) string {
	for _, s := range v {
		if s != "" && s != "<nil>" {
			return s
		}
	}
	return ""
}

func mapStr(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	s := fmt.Sprint(m[k])
	if s == "<nil>" {
		return ""
	}
	return s
}

// ShadowrocketProxyLine renders one [Proxy] line; "" for a type the app does
// not have.
func ShadowrocketProxyLine(p proxynode.Proxy) string {
	if p.Type != "vless" && p.Type != "snell" {
		return proxyLine(p)
	}
	var parts []string
	add := func(k, v string) {
		if v != "" {
			if strings.ContainsAny(v, ",\"\r\n") {
				v = confQuote(v)
			}
			parts = append(parts, k+"="+v)
		}
	}
	addBool := func(k string, v bool) {
		if v {
			parts = append(parts, k+"=true")
		}
	}
	if p.Type == "snell" {
		// Shadowrocket's native [Proxy] syntax uses password, not Surge's psk.
		parts = append(parts, "snell", p.Server, strconv.Itoa(p.Port))
		add("password", p.Str("psk"))
		add("version", strconv.Itoa(orInt(p.Int("version"), 4)))
		if oo := p.Sub("obfs-opts"); oo != nil {
			add("obfs", mapStr(oo, "mode"))
			add("obfs-host", mapStr(oo, "host"))
		}
		if p.Bool("udp") {
			add("udp", "1")
		}
		addBool("reuse", p.Bool("reuse"))
		addBool("tfo", p.Bool("tfo"))
		return confIdent(p.Name) + " = " + strings.Join(parts, ", ")
	}
	parts = append(parts, "vless", p.Server, strconv.Itoa(p.Port))
	add("encrypt-method", nonempty(p.Str("encryption"), "none"))
	add("password", p.Str("uuid"))
	net := p.Str("network")
	if net == "" || net == "tcp" {
		add("obfs", "none")
	} else {
		add("obfs", net)
	}
	if net == "ws" {
		if o := p.Sub("ws-opts"); o != nil {
			add("obfs-uri", mapStr(o, "path"))
			if h, ok := o["headers"].(map[string]any); ok {
				add("obfs-host", mapStr(h, "Host"))
			}
		}
	}
	addBool("udp-relay", true)
	add("peer", nonempty(p.Str("servername"), p.Str("sni")))
	if ro := p.Sub("reality-opts"); ro != nil {
		addBool("tls", true)
		addBool("reality", true)
		add("pbk", mapStr(ro, "public-key"))
		add("sid", mapStr(ro, "short-id"))
	} else if p.Bool("tls") {
		addBool("tls", true)
	}
	if p.Bool("skip-cert-verify") {
		add("allow-insecure", "true")
	}
	add("flow", p.Str("flow"))
	add("fp", p.Str("client-fingerprint"))
	return confIdent(p.Name) + " = " + strings.Join(parts, ", ")
}

// ruleFields splits a rule on the commas outside parentheses, so the
// sub-rules of AND / OR / NOT stay whole.
func ruleFields(rule string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range rule {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, rule[start:i])
				start = i + 1
			}
		}
	}
	return append(out, rule[start:])
}

// rejectProxyPolicy points every rule whose policy is PROXY at REJECT. In
// Shadowrocket PROXY is the node picked on the home page, which no profile
// defines: a user left without lines must stop working, not go through
// whatever else their app holds.
func rejectProxyPolicy(conf string) string {
	lines := strings.Split(conf, "\n")
	inRules := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			inRules = strings.EqualFold(t, "[Rule]")
			continue
		}
		if !inRules || t == "" || confComment(l) {
			continue
		}
		f := ruleFields(l)
		at := 2
		if typ := strings.ToUpper(strings.TrimSpace(f[0])); typ == "FINAL" || typ == "MATCH" {
			at = 1
		}
		if at >= len(f) {
			continue
		}
		policy, note, noted := strings.Cut(f[at], "//")
		if !strings.EqualFold(strings.TrimSpace(policy), "PROXY") {
			continue
		}
		f[at] = EmptyGroupPolicy
		if noted {
			f[at] += " //" + note
		}
		lines[i] = strings.Join(f, ",")
	}
	return strings.Join(lines, "\n")
}

// RenderShadowrocket fills a Shadowrocket profile (see shadowrocketProfile)
// with a user's lines, where it says {{PROXIES}}. A user without lines gets
// REJECT wherever a rule says PROXY.
func RenderShadowrocket(b *Bundle, profile string) (*Rendered, error) {
	var lines []string
	for _, p := range b.Proxies {
		if l := ShadowrocketProxyLine(p); l != "" {
			lines = append(lines, l)
		}
	}
	out := strings.ReplaceAll(profile, "{{PROXIES}}", strings.Join(lines, "\n"))
	if len(lines) == 0 {
		out = rejectProxyPolicy(out)
	}
	return &Rendered{Body: []byte(out), ContentType: "text/plain; charset=utf-8", Filename: b.Name + ".conf", Format: FormatShadowrocket}, nil
}
