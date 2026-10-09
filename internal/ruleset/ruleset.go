// Package ruleset is the one list of routing rules every client's profile is
// built from.
package ruleset

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// A rule set is written once, in terms no client owns: what to match, and
// whether it goes through the line the user picked, straight out, or nowhere.
// Every client family gets its own profile built from the same lines, so
// there is one thing to maintain and nothing to keep in step by hand.

// What a rule does with a connection.
const (
	ActionProxy  = "PROXY"  // through the line the user picked
	ActionDirect = "DIRECT" // without the proxy
	ActionReject = "REJECT"
)

// DefaultGroup names the selector of lines in clients that show one.
const DefaultGroup = "节点选择"

// Default is what a user without a rule set gets: the local network
// stays local, everything else takes the chosen line.
const Default = `IP-CIDR,127.0.0.0/8,DIRECT,no-resolve
IP-CIDR,10.0.0.0/8,DIRECT,no-resolve
IP-CIDR,172.16.0.0/12,DIRECT,no-resolve
IP-CIDR,192.168.0.0/16,DIRECT,no-resolve
IP-CIDR,169.254.0.0/16,DIRECT,no-resolve
IP-CIDR6,fc00::/7,DIRECT,no-resolve
IP-CIDR6,fe80::/10,DIRECT,no-resolve
MATCH,PROXY
`

// Rule is one line of a rule set.
type Rule struct {
	Type      string // DOMAIN, DOMAIN-SUFFIX, DOMAIN-KEYWORD, IP-CIDR, IP-CIDR6, GEOIP, RULE-SET, MATCH
	Value     string // empty for MATCH
	Action    string
	NoResolve bool
}

// List is a published list a RULE-SET rule refers to by name. The lists
// are served next to the panel, at <site>/rules/clash/<name>.yaml and
// <site>/rules/surge/<name>.list, and refreshed there daily; they are
// Loyalsoldier's clash-rules.
type List struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"` // domain | ipcidr | classical
	About string `json:"about"`
}

// Lists is every list a rule set may name. A classical list matches by
// process as well, which only Clash clients can do; the others skip it.
var Lists = []List{
	{"direct", "domain", "国内域名"},
	{"proxy", "domain", "国外常用域名"},
	{"reject", "domain", "广告和跟踪域名"},
	{"private", "domain", "局域网和保留域名"},
	{"apple", "domain", "苹果在国内可直连的域名"},
	{"icloud", "domain", "iCloud 域名"},
	{"google", "domain", "谷歌在国内有服务器的域名（多数时候仍然连不上，一般走代理）"},
	{"tld-not-cn", "domain", "不在国内使用的顶级域名"},
	{"cncidr", "ipcidr", "国内 IP 段"},
	{"lancidr", "ipcidr", "局域网和保留 IP 段"},
	{"telegramcidr", "ipcidr", "Telegram 的 IP 段"},
	{"applications", "classical", "应当直连的常见软件（下载工具等，只有 Clash 类客户端认）"},
}

// ListNamed looks a list up by name.
func ListNamed(name string) (List, bool) {
	for _, l := range Lists {
		if l.Name == name {
			return l, true
		}
	}
	return List{}, false
}

var (
	ruleHost  = regexp.MustCompile(`^[^\s,"'#]{1,253}$`)
	ruleGeoIP = regexp.MustCompile(`^[A-Za-z]{2,16}$`)
)

// Parse reads a rule set. Blank lines and comments are skipped, FINAL is
// read as MATCH, and a set that does not end in MATCH gets MATCH,PROXY.
func Parse(text string) ([]Rule, error) {
	var out []Rule
	matched := false
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || comment(line) {
			continue
		}
		bad := func(msg string) ([]Rule, error) { return nil, fmt.Errorf("第 %d 行（%s）：%s", i+1, line, msg) }
		if matched {
			return bad("MATCH 是最后一条，后面不能再有规则")
		}
		f := strings.Split(line, ",")
		for k := range f {
			f[k] = strings.TrimSpace(f[k])
		}
		r := Rule{Type: strings.ToUpper(f[0])}
		if r.Type == "FINAL" {
			r.Type = "MATCH"
		}
		rest := f[1:]
		if r.Type != "MATCH" {
			if len(rest) == 0 || rest[0] == "" {
				return bad("缺少要匹配的内容")
			}
			r.Value, rest = rest[0], rest[1:]
		}
		if len(rest) == 0 {
			return bad("缺少动作，写 PROXY、DIRECT 或 REJECT")
		}
		switch r.Action = strings.ToUpper(rest[0]); r.Action {
		case ActionProxy, ActionDirect, ActionReject:
		default:
			return bad("动作只能是 PROXY（走所选线路）、DIRECT（直连）或 REJECT（拒绝）")
		}
		byAddress := false
		switch r.Type {
		case "DOMAIN", "DOMAIN-SUFFIX", "DOMAIN-KEYWORD":
			if !ruleHost.MatchString(r.Value) {
				return bad("域名里有不该出现的字符")
			}
		case "IP-CIDR", "IP-CIDR6":
			p, err := netip.ParsePrefix(r.Value)
			if err != nil || p.Addr().Is4() != (r.Type == "IP-CIDR") {
				return bad("不是有效的 IP 段，写成 10.0.0.0/8 或 fc00::/7 这样；IPv6 用 IP-CIDR6")
			}
			byAddress = true
		case "GEOIP":
			if !ruleGeoIP.MatchString(r.Value) {
				return bad("GEOIP 后面是国家或地区代码，例如 CN")
			}
			byAddress = true
		case "RULE-SET":
			l, ok := ListNamed(r.Value)
			if !ok {
				return bad("没有叫这个名字的名单，可用的有：" + listNames())
			}
			byAddress = l.Kind == "ipcidr"
		case "MATCH":
			matched = true
		default:
			return bad("不认识的规则类型，可以用 DOMAIN、DOMAIN-SUFFIX、DOMAIN-KEYWORD、IP-CIDR、IP-CIDR6、GEOIP、RULE-SET、MATCH")
		}
		for _, opt := range rest[1:] {
			if !strings.EqualFold(opt, "no-resolve") || !byAddress {
				return bad("动作后面只能跟 no-resolve，而且只用在按 IP 匹配的规则上")
			}
			r.NoResolve = true
		}
		out = append(out, r)
	}
	if !matched {
		out = append(out, Rule{Type: "MATCH", Action: ActionProxy})
	}
	return out, nil
}

func listNames() string {
	names := make([]string, len(Lists))
	for i, l := range Lists {
		names[i] = l.Name
	}
	return strings.Join(names, "、")
}

// comment reports whether a line is a remark.
func comment(line string) bool {
	return strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "//")
}

// FromClashProfile reads the rules and the name of the first selector out of
// a Clash profile, for rule sets written before there was one list for every
// client. A policy other than DIRECT and REJECT was a group of lines and
// becomes PROXY; a line this package cannot express is left out.
func FromClashProfile(profile string) (rules, group string) {
	var doc struct {
		Groups []struct {
			Name string `yaml:"name"`
		} `yaml:"proxy-groups"`
		Rules []string `yaml:"rules"`
	}
	if yaml.Unmarshal([]byte(profile), &doc) != nil {
		return "", ""
	}
	if len(doc.Groups) > 0 {
		group = strings.TrimSpace(doc.Groups[0].Name)
	}
	action := func(policy string) string {
		switch p := strings.ToUpper(strings.TrimSpace(policy)); {
		case p == ActionDirect:
			return ActionDirect
		case strings.HasPrefix(p, ActionReject):
			return ActionReject
		}
		return ActionProxy
	}
	var out []string
	for _, line := range doc.Rules {
		f := strings.Split(line, ",")
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		typ := strings.ToUpper(f[0])
		at := 2
		if typ == "MATCH" || typ == "FINAL" {
			typ, at = "MATCH", 1
		}
		if len(f) <= at {
			continue
		}
		parts := append(append([]string{typ}, f[1:at]...), action(f[at]))
		for _, opt := range f[at+1:] {
			if strings.EqualFold(opt, "no-resolve") {
				parts = append(parts, "no-resolve")
				break
			}
		}
		one := strings.Join(parts, ",")
		if _, err := Parse(one); err != nil {
			continue
		}
		out = append(out, one)
		if typ == "MATCH" {
			break
		}
	}
	if len(out) == 0 {
		return "", group
	}
	return strings.Join(out, "\n") + "\n", group
}
