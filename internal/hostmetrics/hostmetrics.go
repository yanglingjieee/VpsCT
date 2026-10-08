// Package hostmetrics reads what a Linux host says about itself in /proc and
// /sys. Nothing here needs root: the agent's heartbeat and its unprivileged
// live worker read the same numbers the same way.
package hostmetrics

import "strings"

// Memory is the host's memory and swap in bytes. Used is what applications
// hold: total less what the kernel could hand out without swapping.
type Memory struct {
	Total, Used, SwapTotal, SwapUsed int64
}

// Counted reports whether an interface's traffic counts as the host's: not
// loopback, not a bridge or tunnel the host itself made, and, when iface is
// given, only that one.
func Counted(name, iface string) bool {
	return name != "lo" && !strings.HasPrefix(name, "docker") && !strings.HasPrefix(name, "veth") && !strings.HasPrefix(name, "br-") && !strings.HasPrefix(name, "tun") && !strings.HasPrefix(name, "wg") && (iface == "" || name == iface)
}
