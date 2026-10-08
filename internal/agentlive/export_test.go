package agentlive

import (
	"context"
	"net/netip"
)

// ProbeAnywhere lets a test probe the machine it runs on: every address is
// allowed and every name is that machine.
func ProbeAnywhere() {
	public = func(netip.Addr) bool { return true }
	lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
}
