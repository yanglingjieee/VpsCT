package subscription

import (
	"strings"
	"time"

	"ctlvps/internal/proxynode"
)

// Formats.
const (
	FormatMihomo       = "mihomo"
	FormatClash        = "clash" // alias of mihomo
	FormatRaw          = "raw"   // base64 share links
	FormatURIList      = "uri"   // plain share links
	FormatShadowrocket = "shadowrocket"
)

// KnownFormats lists the output formats: a profile for the two client
// families the panel builds one for, and the plain list of lines.
var KnownFormats = []string{FormatMihomo, FormatShadowrocket, FormatRaw, FormatURIList}

// NormalizeFormat maps aliases to canonical names ("" -> "").
func NormalizeFormat(f string) string {
	switch strings.ToLower(strings.TrimSpace(f)) {
	case FormatClash, FormatMihomo, "clash.meta", "meta":
		return FormatMihomo
	case FormatRaw, "base64", "v2ray", "v2rayn", "v2rayng", "v2rayu":
		return FormatRaw
	case FormatURIList, "uri-list", "links", "plain":
		return FormatURIList
	case FormatShadowrocket, "rocket":
		return FormatShadowrocket
	}
	return ""
}

// DetectFormat guesses the client format from its User-Agent. A client that
// reads neither profile gets the list of lines if it is known to take one.
func DetectFormat(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case strings.Contains(l, "shadowrocket"):
		return FormatShadowrocket
	case strings.Contains(l, "clash"), strings.Contains(l, "mihomo"), strings.Contains(l, "stash"), strings.Contains(l, "verge"), strings.Contains(l, "flclash"), strings.Contains(l, "nyanpasu"):
		return FormatMihomo
	case strings.Contains(l, "v2ray"), strings.Contains(l, "nekobox"), strings.Contains(l, "nekoray"), strings.Contains(l, "quantumult"), strings.Contains(l, "loon"),
		strings.Contains(l, "hiddify"), strings.Contains(l, "karing"):
		return FormatRaw
	}
	return ""
}

// Bundle is what a user's profile is made of, whatever the client.
type Bundle struct {
	Name        string
	Proxies     []proxynode.Proxy
	Userinfo    *Userinfo
	InfoNodes   []string
	GeneratedAt time.Time
	// Order, when set, is what users choose from, in menu order.
	Order []string
}

// EmptyGroupPolicy stands in where there is no line to use. A profile whose
// lines are gone must stop working, not quietly connect directly.
const EmptyGroupPolicy = "REJECT"

// AllProxyNames returns the selectable names: Order when set, otherwise the
// proxies in output order.
func (b *Bundle) AllProxyNames() []string {
	out := make([]string, 0, len(b.Proxies))
	for _, p := range b.Proxies {
		out = append(out, p.Name)
	}
	if len(b.Order) == 0 {
		return out
	}
	exists := make(map[string]bool, len(out))
	for _, n := range out {
		exists[n] = true
	}
	ordered := make([]string, 0, len(b.Order))
	for _, n := range b.Order {
		if exists[n] {
			ordered = append(ordered, n)
			exists[n] = false
		}
	}
	return ordered
}
