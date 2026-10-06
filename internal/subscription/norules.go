package subscription

// NoRulesGroup is the single group of the built-in "no rules" profiles.
const NoRulesGroup = "节点选择"

// NoRules returns the built-in profile for a template kind: every line in one
// selector and everything except the local network through it. It is what a
// user without a rule set gets, and what a rule set falls back to for a
// client family it was not written for.
func NoRules(kind string) string {
	switch kind {
	case "shadowrocket":
		return noRulesShadowrocket
	case "surge":
		return noRulesSurge
	case "singbox":
		return noRulesSingBox
	}
	return noRulesMihomo
}

const noRulesMihomo = `mixed-port: 7890
allow-lan: false
mode: rule
log-level: warning
ipv6: true
unified-delay: true
tcp-concurrent: true
profile:
  store-selected: true
  store-fake-ip: true
dns:
  enable: true
  ipv6: false
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  fake-ip-filter:
    - "*.lan"
    - "*.local"
    - +.msftconnecttest.com
    - +.msftncsi.com
    - time.*.com
    - ntp.*.com
  default-nameserver: [223.5.5.5, 119.29.29.29]
  nameserver:
    - https://1.1.1.1/dns-query#节点选择
    - https://8.8.8.8/dns-query#节点选择
  direct-nameserver: [https://doh.pub/dns-query, https://dns.alidns.com/dns-query]
  proxy-server-nameserver: [https://doh.pub/dns-query, https://dns.alidns.com/dns-query]
proxies: []
proxy-groups:
  - name: 节点选择
    type: select
    proxies:
      - "{{all}}"
rules:
  - IP-CIDR,127.0.0.0/8,DIRECT,no-resolve
  - IP-CIDR,10.0.0.0/8,DIRECT,no-resolve
  - IP-CIDR,172.16.0.0/12,DIRECT,no-resolve
  - IP-CIDR,192.168.0.0/16,DIRECT,no-resolve
  - IP-CIDR,169.254.0.0/16,DIRECT,no-resolve
  - IP-CIDR6,fc00::/7,DIRECT,no-resolve
  - IP-CIDR6,fe80::/10,DIRECT,no-resolve
  - MATCH,节点选择
`

const noRulesShadowrocket = `[General]
bypass-system = true
skip-proxy = 192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12, localhost, *.local, captive.apple.com
tun-excluded-routes = 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.0.0.0/24, 192.0.2.0/24, 192.88.99.0/24, 192.168.0.0/16, 198.51.100.0/24, 203.0.113.0/24, 224.0.0.0/4, 255.255.255.255/32, 239.255.255.250/32
dns-server = https://doh.pub/dns-query, https://dns.alidns.com/dns-query, 223.5.5.5
fallback-dns-server = system
ipv6 = false
prefer-ipv6 = false
dns-direct-system = false
icmp-auto-reply = true
private-ip-answer = true
dns-direct-fallback-proxy = true
udp-policy-not-supported-behaviour = REJECT

[Proxy]
{{PROXIES}}

[Proxy Group]
节点选择 = select,{{all}}

[Rule]
IP-CIDR,127.0.0.0/8,DIRECT
IP-CIDR,10.0.0.0/8,DIRECT
IP-CIDR,172.16.0.0/12,DIRECT
IP-CIDR,192.168.0.0/16,DIRECT
FINAL,节点选择

[Host]
localhost = 127.0.0.1
`

const noRulesSurge = `[General]
loglevel = notify
skip-proxy = 127.0.0.1, 192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12, localhost, *.local, captive.apple.com
dns-server = 223.5.5.5, 119.29.29.29

[Proxy]
{{PROXIES}}

[Proxy Group]
节点选择 = select,{{all}}

[Rule]
IP-CIDR,127.0.0.0/8,DIRECT
IP-CIDR,10.0.0.0/8,DIRECT
IP-CIDR,172.16.0.0/12,DIRECT
IP-CIDR,192.168.0.0/16,DIRECT
FINAL,节点选择
`

const noRulesSingBox = `{
  "log": { "level": "warn", "timestamp": true },
  "dns": {
    "servers": [
      { "tag": "dns-local", "type": "udp", "server": "223.5.5.5" },
      { "tag": "dns-remote", "type": "https", "server": "1.1.1.1", "domain_resolver": "dns-local", "detour": "节点选择" }
    ],
    "final": "dns-remote",
    "strategy": "ipv4_only"
  },
  "inbounds": [
    { "type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 7890 },
    { "type": "tun", "tag": "tun-in", "address": ["172.19.0.1/30", "fdfe:dcba:9876::1/126"], "auto_route": true, "strict_route": true, "stack": "system" }
  ],
  "outbounds": [
    { "type": "selector", "tag": "节点选择", "outbounds": ["{{all}}"] },
    { "type": "direct", "tag": "direct" }
  ],
  "route": {
    "auto_detect_interface": true,
    "default_domain_resolver": "dns-local",
    "final": "节点选择",
    "rules": [
      { "action": "sniff" },
      { "protocol": "dns", "action": "hijack-dns" },
      { "ip_is_private": true, "outbound": "direct" }
    ]
  },
  "experimental": { "cache_file": { "enabled": true } }
}`
