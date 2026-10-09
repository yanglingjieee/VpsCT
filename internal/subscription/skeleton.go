package subscription

// What a profile is besides its lines and rules: how the client listens,
// resolves names and sniffs. It is the same for every rule set and is not
// edited in the panel; the lines, the selector and the rules are added to it.

// skeletonMihomo is the Clash / Mihomo profile without lines and rules.
// Foreign names are resolved through the chosen line (the nameservers are
// filled in with the selector's name), IPv6 answers are not returned, and
// what a connection is for is read from its first bytes where the client
// only has an address.
const skeletonMihomo = `mixed-port: 7890
allow-lan: false
mode: rule
log-level: warning
ipv6: true
unified-delay: true
tcp-concurrent: true
find-process-mode: strict
keep-alive-interval: 30
external-controller: 127.0.0.1:9090
profile:
  store-selected: true
  store-fake-ip: true
geo-auto-update: false
sniffer:
  enable: true
  force-dns-mapping: true
  parse-pure-ip: true
  sniff:
    HTTP: {ports: [80, 8080-8880], override-destination: true}
    TLS: {ports: [443, 8443]}
    QUIC: {ports: [443, 8443]}
  skip-domain: [Mijia Cloud, +.push.apple.com]
dns:
  enable: true
  ipv6: false
  listen: 127.0.0.1:1053
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  fake-ip-filter:
    - "*.lan"
    - "*.local"
    - +.msftconnecttest.com
    - +.msftncsi.com
    - time.*.com
    - ntp.*.com
    - +.stun.*.*
    - +.stun.*.*.*
    - localhost.ptlogin2.qq.com
    - +.srv.nintendo.net
    - +.stun.playstation.net
    - xbox.*.microsoft.com
    - +.xboxlive.com
    - +.market.xiaomi.com
  default-nameserver: [223.5.5.5, 119.29.29.29]
  nameserver: []
  nameserver-policy: {}
  direct-nameserver: [https://doh.pub/dns-query, https://dns.alidns.com/dns-query]
  proxy-server-nameserver: [https://doh.pub/dns-query, https://dns.alidns.com/dns-query]
proxies: []
`

// skeletonShadowrocket is the [General] section of the Shadowrocket profile.
const skeletonShadowrocket = `[General]
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
`
