package subscription

import (
	"fmt"
	"strconv"
	"strings"

	"ctlvps/internal/proxynode"
)

func confQuote(s string) string {
	s = strings.NewReplacer("\n", " ", "\r", " ", "\x00", "").Replace(s)
	return `"` + strings.ReplaceAll(s, `"`, `'`) + `"`
}

func confIdent(name string) string {
	name = strings.NewReplacer("\n", " ", "\r", " ", "\x00", "").Replace(name)
	if name == "" {
		return name
	}
	if strings.ContainsAny(name, ",=\"") {
		return confQuote(name)
	}
	return name
}

// proxyLine renders a [Proxy] line in the form Surge gave these node types
// and Shadowrocket reads too; "" for a type that form does not have.
func proxyLine(p proxynode.Proxy) string {
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
	host, port := p.Server, strconv.Itoa(p.Port)
	switch p.Type {
	case "ss":
		parts = append(parts, "ss", host, port)
		add("encrypt-method", p.Str("cipher"))
		add("password", p.Str("password"))
		addBool("udp-relay", true)
		if p.Str("plugin") == "obfs" {
			o := p.Sub("plugin-opts")
			add("obfs", fmt.Sprint(o["mode"]))
			if h := fmt.Sprint(o["host"]); h != "" && h != "<nil>" {
				add("obfs-host", h)
			}
		}
	case "vmess":
		parts = append(parts, "vmess", host, port)
		add("username", p.Str("uuid"))
		addBool("tls", p.Bool("tls"))
		add("sni", p.Str("servername"))
		addBool("skip-cert-verify", p.Bool("skip-cert-verify"))
		if p.Str("network") == "ws" {
			addBool("ws", true)
			if o := p.Sub("ws-opts"); o != nil {
				add("ws-path", fmt.Sprint(o["path"]))
				if h, ok := o["headers"].(map[string]any); ok {
					if hv := fmt.Sprint(h["Host"]); hv != "" && hv != "<nil>" {
						add("ws-headers", "Host:"+hv)
					}
				}
			}
		}
		addBool("vmess-aead", p.Int("alterId") == 0)
	case "trojan":
		parts = append(parts, "trojan", host, port)
		add("password", p.Str("password"))
		add("sni", p.Str("sni"))
		addBool("skip-cert-verify", p.Bool("skip-cert-verify"))
		if p.Str("network") == "ws" {
			addBool("ws", true)
			if o := p.Sub("ws-opts"); o != nil {
				add("ws-path", fmt.Sprint(o["path"]))
			}
		}
	case "hysteria2":
		parts = append(parts, "hysteria2", host, port)
		add("password", p.Str("password"))
		add("sni", p.Str("sni"))
		addBool("skip-cert-verify", p.Bool("skip-cert-verify"))
		if d := p.Str("down"); d != "" {
			add("download-bandwidth", strings.TrimSuffix(strings.TrimSpace(d), " Mbps"))
		}
		add("port-hopping", p.Str("ports"))
	case "tuic":
		parts = append(parts, "tuic-v5", host, port)
		add("uuid", p.Str("uuid"))
		add("password", p.Str("password"))
		add("sni", p.Str("sni"))
		if alpn := p.StrList("alpn"); len(alpn) > 0 {
			add("alpn", alpn[0])
		}
		addBool("skip-cert-verify", p.Bool("skip-cert-verify"))
	case "anytls":
		parts = append(parts, "anytls", host, port)
		add("password", p.Str("password"))
		sni := p.Str("sni")
		if sni == "" {
			sni = host
		}
		add("sni", sni)
		addBool("skip-cert-verify", p.Bool("skip-cert-verify"))
	case "socks5":
		if p.Bool("tls") {
			parts = append(parts, "socks5-tls", host, port)
		} else {
			parts = append(parts, "socks5", host, port)
		}
		if u := p.Str("username"); u != "" {
			parts = append(parts, u, p.Str("password"))
		}
	case "http":
		if p.Bool("tls") {
			parts = append(parts, "https", host, port)
		} else {
			parts = append(parts, "http", host, port)
		}
		if u := p.Str("username"); u != "" {
			parts = append(parts, u, p.Str("password"))
		}
	default:
		return ""
	}
	if p.Type != "socks5" && p.Type != "http" {
		if p.Bool("tfo") {
			addBool("tfo", true)
		}
	}
	return confIdent(p.Name) + " = " + strings.Join(parts, ", ")
}

func orInt(n, d int) int {
	if n == 0 {
		return d
	}
	return n
}

// confComment reports whether a line of a profile is a remark.
func confComment(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") || strings.HasPrefix(t, "//")
}
