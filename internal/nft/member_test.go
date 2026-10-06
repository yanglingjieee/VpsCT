package nft

import (
	"strings"
	"testing"

	"ctlvps/internal/agentproto"
)

func TestMemberAccounting(t *testing.T) {
	nodes := []agentproto.NodeSpec{
		{NodeID: 5, ListenPort: 443, Core: "singbox"},
		{NodeID: 20, Core: "singbox", AttachTo: 5},
		{NodeID: 21, Core: "singbox", AttachTo: 5, Blocked: true},
	}
	rules, err := NodeRules(nodes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(rules, "th dport") != 1 || !strings.Contains(rules, "th dport 443 ") {
		t.Fatalf("only the listener marks client connections:\n%s", rules)
	}
	for _, want := range []string{
		"add counter inet ctlvps_nodes n20_rx", "add counter inet ctlvps_nodes n21_tx",
		"ct mark 0x43000014 counter name n20_rx return", "ct mark 0x43000014 counter name n20_tx return",
		"ct mark 0x43000015 counter name n21_rx drop", "output ct mark 0x43000015 drop",
	} {
		if !strings.Contains(rules, want) {
			t.Fatalf("missing %q:\n%s", want, rules)
		}
	}
	ingress, err := IngressRules(ingressSnapshot(), []agentproto.NodeSpec{{NodeID: 5, Protocol: "vless", ListenPort: 443, Core: "singbox"}, {NodeID: 20, Protocol: "vless", Core: "singbox", AttachTo: 5}})
	if err != nil || !strings.Contains(ingress, "tcp dport { 443 }") {
		t.Fatalf("a member has no port to open: %v\n%s", err, ingress)
	}
}
