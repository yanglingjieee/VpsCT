// Package liveproto is the contract of the live channel: one WebSocket from
// each agent's network worker to ctlvpsd, opened by the agent. The worker
// sends what the host is doing every second or so and how long the probe
// targets take to answer; the controller sends back how often it wants both.
// Each message is one JSON object in a text frame. Keys are short because a
// reading is sent every second from hosts whose traffic is billed.
package liveproto

import (
	"errors"
	"net/netip"
	"strings"
)

// Path is where the worker connects, with the agent's bearer token.
const Path = "/api/agent/v1/live"

// Bounds both ends hold the other to.
const (
	MaxUpBytes   = 8 << 10
	MaxDownBytes = 32 << 10
	MaxTargets   = 24

	MinIntervalMs = 500
	MaxIntervalMs = 30_000
	// A viewer is watching: readings every second. Nobody is: every five.
	WatchedIntervalMs = 1000
	IdleIntervalMs    = 5000

	MinProbeIntervalSec = 5
	MaxProbeIntervalSec = 300
	// The kernel sends a lost SYN again after one second. A probe that has no
	// answer just before that lost a packet on the way there or back.
	ProbeTimeoutMs = 950
)

// Host is what does not change from one reading to the next.
type Host struct {
	OS       string `json:"os,omitempty"` // "Debian GNU/Linux 12 (bookworm)"
	Kernel   string `json:"kernel,omitempty"`
	Arch     string `json:"arch,omitempty"`
	Virt     string `json:"virt,omitempty"` // kvm, lxc, ...; empty when not known
	CPUModel string `json:"cpu_model,omitempty"`
	Cores    int    `json:"cores,omitempty"`
}

// Sample is one reading of the host. Rates are bytes per second since the
// reading before, on the interfaces the agent counts for the server's traffic.
type Sample struct {
	CPU       float64 `json:"c"` // percent of all cores
	MemUsed   int64   `json:"mu"`
	MemTotal  int64   `json:"mt"`
	SwapUsed  int64   `json:"su,omitempty"`
	SwapTotal int64   `json:"st,omitempty"`
	DiskUsed  int64   `json:"du"`
	DiskTotal int64   `json:"dt"`
	Load1     float64 `json:"l1"`
	Load5     float64 `json:"l5"`
	Load15    float64 `json:"l15"`
	RxRate    int64   `json:"rx"`
	TxRate    int64   `json:"tx"`
	TCP       int     `json:"tc"`
	UDP       int     `json:"uc"`
	Uptime    int64   `json:"up"`
}

// ProbeResult is how long one target took to accept a TCP connection, in
// microseconds; Lost when it did not in time.
type ProbeResult struct {
	Target int64 `json:"i"`
	RTT    int64 `json:"u"`
}

// Lost is the RTT of a probe that got no answer.
const Lost = -1

// Up is a message from the worker. A connection starts with Host.
type Up struct {
	Host   *Host         `json:"h,omitempty"`
	Sample *Sample       `json:"s,omitempty"`
	Probes []ProbeResult `json:"p,omitempty"`
}

// Target is one address to probe.
type Target struct {
	ID   int64  `json:"i"`
	Host string `json:"h"`
	Port int    `json:"p"`
}

// Plan is what to probe and how often each target.
type Plan struct {
	IntervalSec int      `json:"every"`
	Targets     []Target `json:"targets"`
}

// Down is a message from the controller. An empty one keeps the connection
// from looking dead; the fields that are set replace what the worker had.
type Down struct {
	IntervalMs int   `json:"interval_ms,omitempty"`
	Plan       *Plan `json:"plan,omitempty"`
}

// ClampInterval keeps a reading interval the controller asked for in bounds.
func ClampInterval(ms int) int { return max(MinIntervalMs, min(MaxIntervalMs, ms)) }

// Validate holds a plan to the bounds the worker accepts. The worker checks
// what it is sent: it must not become a port scanner for whoever answers as
// the controller.
func (p *Plan) Validate() error {
	if p.IntervalSec < MinProbeIntervalSec || p.IntervalSec > MaxProbeIntervalSec {
		return errors.New("probe interval out of range")
	}
	if len(p.Targets) > MaxTargets {
		return errors.New("too many probe targets")
	}
	seen := map[int64]bool{}
	for _, t := range p.Targets {
		if t.ID <= 0 || seen[t.ID] {
			return errors.New("probe target ids must be positive and distinct")
		}
		seen[t.ID] = true
		if err := ValidateTarget(t.Host, t.Port); err != nil {
			return err
		}
	}
	return nil
}

// ValidateTarget accepts a public host name or address and a port.
func ValidateTarget(host string, port int) error {
	if port < 1 || port > 65535 {
		return errors.New("probe port out of range")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !Public(ip) {
			return errors.New("probe target is not a public address")
		}
		return nil
	}
	if len(host) == 0 || len(host) > 253 || !strings.Contains(host, ".") {
		return errors.New("probe target is not a host name")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("probe target is not a host name")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return errors.New("probe target is not a host name")
			}
		}
	}
	return nil
}

var notPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
}

// Public reports whether an address is one on the internet: a probe never
// goes to the host itself, its own network or its provider's metadata service.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range notPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
