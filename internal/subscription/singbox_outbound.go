package subscription

// How a node is dialed by sing-box as a client. The panel builds no sing-box
// profile; the integration harness uses this to connect to the inbounds the
// agent sets up, with the same parameters a user's client would get.

import (
	"ctlvps/internal/proxynode"
	"ctlvps/internal/sshconfig"
	"ctlvps/internal/wgconfig"
	"fmt"
	"strconv"
	"strings"
)

// SingBoxOutbound converts a proxy into a sing-box client outbound. ok=false
// when the protocol has no sing-box outbound (e.g. snell).
func SingBoxOutbound(p proxynode.Proxy, via string) (map[string]any, bool) {
	o := map[string]any{"tag": p.Name, "server": p.Server, "server_port": p.Port}
	tls := func(serverNameKey string, force bool) map[string]any {
		if !force && !p.Bool("tls") {
			return nil
		}
		t := map[string]any{"enabled": true}
		if sn := p.Str(serverNameKey); sn != "" {
			t["server_name"] = sn
		}
		if p.Bool("skip-cert-verify") {
			t["insecure"] = true
		}
		if alpn := p.StrList("alpn"); len(alpn) > 0 {
			t["alpn"] = alpn
		}
		if fp := p.Str("client-fingerprint"); fp != "" {
			t["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
		if ro := p.Sub("reality-opts"); ro != nil {
			r := map[string]any{"enabled": true, "public_key": ro["public-key"]}
			if sid, ok := ro["short-id"]; ok && fmt.Sprint(sid) != "" {
				r["short_id"] = fmt.Sprint(sid)
			}
			t["reality"] = r
			if _, ok := t["utls"]; !ok {
				t["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
			}
		}
		return t
	}
	transport := func() map[string]any {
		switch p.Str("network") {
		case "ws", "httpupgrade":
			tr := map[string]any{"type": p.Str("network")}
			if wo := p.Sub("ws-opts"); wo != nil {
				if path := fmt.Sprint(wo["path"]); path != "" && path != "<nil>" {
					tr["path"] = path
				}
				if h, ok := wo["headers"].(map[string]any); ok {
					if host := fmt.Sprint(h["Host"]); host != "" && host != "<nil>" {
						if tr["type"] == "ws" {
							tr["headers"] = map[string]any{"Host": host}
						} else {
							tr["host"] = host
						}
					}
				}
				if ed, ok := wo["max-early-data"]; ok {
					tr["max_early_data"] = ed
					tr["early_data_header_name"] = "Sec-WebSocket-Protocol"
				}
			}
			return tr
		case "grpc":
			tr := map[string]any{"type": "grpc"}
			if go_ := p.Sub("grpc-opts"); go_ != nil {
				tr["service_name"] = go_["grpc-service-name"]
			}
			return tr
		case "h2", "http":
			tr := map[string]any{"type": "http"}
			key := "h2-opts"
			if p.Str("network") == "http" {
				key = "http-opts"
			}
			if ho := p.Sub(key); ho != nil {
				if path, ok := ho["path"]; ok {
					if l, ok := path.([]any); ok && len(l) > 0 {
						tr["path"] = l[0]
					} else {
						tr["path"] = path
					}
				}
				if host, ok := ho["host"]; ok {
					tr["host"] = host
				}
			}
			return tr
		}
		return nil
	}
	switch p.Type {
	case "ss":
		o["type"] = "shadowsocks"
		o["method"] = p.Str("cipher")
		o["password"] = p.Str("password")
		if plugin := p.Str("plugin"); plugin != "" {
			po := p.Sub("plugin-opts")
			switch plugin {
			case "obfs":
				o["plugin"] = "obfs-local"
				opts := "obfs=" + fmt.Sprint(po["mode"])
				if h := fmt.Sprint(po["host"]); h != "" && h != "<nil>" {
					opts += ";obfs-host=" + h
				}
				o["plugin_opts"] = opts
			case "v2ray-plugin":
				o["plugin"] = "v2ray-plugin"
				var parts []string
				if toBool(po["tls"]) {
					parts = append(parts, "tls")
				}
				if h := fmt.Sprint(po["host"]); h != "" && h != "<nil>" {
					parts = append(parts, "host="+h)
				}
				if pa := fmt.Sprint(po["path"]); pa != "" && pa != "<nil>" {
					parts = append(parts, "path="+pa)
				}
				o["plugin_opts"] = strings.Join(parts, ";")
			}
		}
		if p.Bool("udp-over-tcp") {
			o["udp_over_tcp"] = true
		}
	case "vmess":
		o["type"] = "vmess"
		o["uuid"] = p.Str("uuid")
		o["security"] = orDefault(p.Str("cipher"), "auto")
		o["alter_id"] = p.Int("alterId")
		if t := tls("servername", false); t != nil {
			o["tls"] = t
		}
		if tr := transport(); tr != nil {
			o["transport"] = tr
		}
	case "vless":
		o["type"] = "vless"
		o["uuid"] = p.Str("uuid")
		if f := p.Str("flow"); f != "" {
			o["flow"] = f
		}
		if t := tls("servername", false); t != nil {
			o["tls"] = t
		}
		if tr := transport(); tr != nil {
			o["transport"] = tr
		}
		if pe := p.Str("packet-encoding"); pe != "" {
			o["packet_encoding"] = pe
		}
	case "trojan":
		o["type"] = "trojan"
		o["password"] = p.Str("password")
		o["tls"] = tls("sni", true)
		if tr := transport(); tr != nil {
			o["transport"] = tr
		}
	case "hysteria2":
		o["type"] = "hysteria2"
		o["password"] = p.Str("password")
		t := tls("sni", true)
		if _, ok := t["alpn"]; !ok {
			t["alpn"] = []string{"h3"}
		}
		o["tls"] = t
		if obfs := p.Str("obfs"); obfs != "" {
			o["obfs"] = map[string]any{"type": obfs, "password": p.Str("obfs-password")}
		}
		if up := mbps(p.Str("up")); up > 0 {
			o["up_mbps"] = up
		}
		if down := mbps(p.Str("down")); down > 0 {
			o["down_mbps"] = down
		}
		if ports := p.Str("ports"); ports != "" {
			o["server_ports"] = strings.Split(strings.ReplaceAll(ports, "-", ":"), ",")
		}
	case "tuic":
		o["type"] = "tuic"
		o["uuid"] = p.Str("uuid")
		o["password"] = p.Str("password")
		o["congestion_control"] = orDefault(p.Str("congestion-controller"), "bbr")
		o["udp_relay_mode"] = orDefault(p.Str("udp-relay-mode"), "native")
		if p.Bool("reduce-rtt") {
			o["zero_rtt_handshake"] = true
		}
		t := tls("sni", true)
		if _, ok := t["alpn"]; !ok {
			t["alpn"] = []string{"h3"}
		}
		o["tls"] = t
	case "anytls":
		o["type"] = "anytls"
		o["password"] = p.Str("password")
		o["tls"] = tls("sni", true)
	case "ssh":
		config, err := sshconfig.Decode(p.Params)
		if err != nil {
			return nil, false
		}
		for key, value := range config.SingBox() {
			o[key] = value
		}
	case "socks5":
		o["type"] = "socks"
		o["version"] = "5"
		if u := p.Str("username"); u != "" {
			o["username"] = u
			o["password"] = p.Str("password")
		}
	case "http":
		o["type"] = "http"
		if u := p.Str("username"); u != "" {
			o["username"] = u
			o["password"] = p.Str("password")
		}
		if t := tls("sni", false); t != nil {
			o["tls"] = t
		}
	default:
		return nil, false
	}
	if via != "" {
		o["detour"] = via
	}
	return o, true
}

func mbps(s string) int {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimSuffix(s, "mbps")
	s = strings.TrimSpace(s)
	n, _ := strconv.Atoi(s)
	return n
}

func toBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1"
	}
	return false
}

// SingBoxEndpoint renders the 1.11+ endpoint shape, never the removed outbound.
// Remote-DNS and TCP-only mihomo semantics have no equivalent endpoint option.
func SingBoxEndpoint(p proxynode.Proxy, via string) (map[string]any, bool) {
	if p.Type != "wireguard" {
		return nil, false
	}
	c, err := wgconfig.Decode(p.Params)
	if err != nil || c.RemoteDNS || !c.UDP {
		return nil, false
	}
	ep := c.Endpoint(p.Server, p.Port)
	ep["tag"] = p.Name
	if via != "" {
		ep["detour"] = via
	}
	return ep, true
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
