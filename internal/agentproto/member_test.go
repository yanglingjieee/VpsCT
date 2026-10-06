package agentproto

import "testing"

func TestValidateMembers(t *testing.T) {
	uuid := "11111111-1111-4111-8111-111111111111"
	build := func(edit func(d *DesiredState)) error {
		d := &DesiredState{ServerID: 1, Revision: 1, Nodes: []NodeSpec{
			{NodeID: 5, Protocol: "vless", Core: "singbox", ListenPort: 443, Params: map[string]any{"uuid": uuid}},
			{NodeID: 6, Protocol: "trojan", Core: "singbox", ListenPort: 8443, Params: map[string]any{"password": "x"}},
			{NodeID: 20, Protocol: "vless", Core: "singbox", AttachTo: 5, Params: map[string]any{"uuid": uuid}},
		}}
		edit(d)
		d.Hash = ContentHash(d)
		return ValidateDesired(d, 1, 0, "")
	}
	if err := build(func(*DesiredState) {}); err != nil {
		t.Fatal("member on a VLESS listener rejected:", err)
	}
	for name, edit := range map[string]func(*DesiredState){
		"own port":          func(d *DesiredState) { d.Nodes[2].ListenPort = 9443 },
		"missing listener":  func(d *DesiredState) { d.Nodes[2].AttachTo = 99 },
		"single-user proto": func(d *DesiredState) { d.Nodes[2].AttachTo = 6 },
		"nested": func(d *DesiredState) {
			d.Nodes = append(d.Nodes, NodeSpec{NodeID: 21, Protocol: "vless", Core: "singbox", AttachTo: 20, Params: map[string]any{"uuid": uuid}})
		},
		"no credential":     func(d *DesiredState) { d.Nodes[2].Params = map[string]any{} },
		"duplicate id":      func(d *DesiredState) { d.Nodes[2].NodeID = 5 },
		"other protocol":    func(d *DesiredState) { d.Nodes[2].Protocol = "trojan" },
		"relay on listener": func(d *DesiredState) { d.Nodes[0].Relay = &RelaySpec{} },
		"broken relay":      func(d *DesiredState) { d.Nodes[2].Relay = &RelaySpec{Server: "203.0.113.7", Port: 443, UUID: uuid} },
	} {
		if err := build(edit); err == nil {
			t.Fatalf("%s: invalid member accepted", name)
		}
	}
}
