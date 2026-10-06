package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ctlvps/internal/agentproto"
	"ctlvps/internal/domain"
)

func memberSpec(id, parent int64, uuid string) agentproto.NodeSpec {
	return agentproto.NodeSpec{NodeID: id, Protocol: "vless", Core: "singbox", AttachTo: parent,
		Params: map[string]any{"uuid": uuid, "flow": "xtls-rprx-vision"}}
}

func TestMembersShareOneInbound(t *testing.T) {
	dir := t.TempDir()
	srv := domain.Server{ID: 1, Name: "la", PublicHost: "203.0.113.10", CoreMode: domain.CoreModeStable, CertMode: "self_signed"}
	ds := &agentproto.DesiredState{ServerID: 1, PublicHost: "203.0.113.10", PreferIPv6: true, Tuning: agentproto.Tuning{GoMemLimitMB: 128}}
	parent := specFor(t, srv, 5, "vless", 443, false)
	other := specFor(t, srv, 6, "vless", 8443, false)
	blocked := memberSpec(22, 5, "33333333-3333-4333-8333-333333333333")
	blocked.Blocked = true
	nodes := []agentproto.NodeSpec{
		memberSpec(21, 5, "22222222-2222-4222-8222-222222222222"), blocked, parent, other,
		memberSpec(20, 5, "11111111-1111-4111-8111-111111111111"),
	}
	d := NewSingBox(Paths{BinDir: dir, ConfDir: dir, LogDir: dir, CertDir: filepath.Join(dir, "certs"), DataDir: dir}, NewSystemd())
	cfg, err := d.BuildResourceConfig(ds, nodes, nil)
	if err != nil {
		t.Fatal(err)
	}
	inbounds := cfg["inbounds"].([]any)
	if len(inbounds) != 2 {
		t.Fatalf("members must not open listeners: %d inbounds", len(inbounds))
	}
	users := inbounds[0].(map[string]any)["users"].([]any)
	var names []string
	for _, u := range users {
		names = append(names, u.(map[string]any)["name"].(string))
	}
	if got, _ := json.Marshal(names); string(got) != `["n5","n20","n21"]` {
		t.Fatalf("owner first, then live members in id order; a blocked member is unknown to the listener: %s", got)
	}
	tags := map[string]any{}
	for _, o := range cfg["outbounds"].([]any) {
		m := o.(map[string]any)
		tags[m["tag"].(string)] = m["routing_mark"]
	}
	if len(tags) != 4 || tags["node-20-direct"] == tags["node-21-direct"] || tags["node-20-direct"] == tags["node-5-direct"] {
		t.Fatalf("every credential needs its own accounting mark: %v", tags)
	}
	if _, ok := tags["node-22-direct"]; ok {
		t.Fatal("blocked member kept an outbound")
	}
	// A member's rule must win over its listener's catch-all.
	catchAll, last := -1, -1
	for i, r := range cfg["route"].(map[string]any)["rules"].([]any) {
		m := r.(map[string]any)
		in, _ := m["inbound"].([]string)
		if len(in) != 1 || in[0] != "node-5" || m["action"] != "route" {
			continue
		}
		if _, member := m["auth_user"]; member {
			last = i
		} else {
			catchAll = i
		}
	}
	if last < 0 || catchAll < last {
		t.Fatalf("member rules at %d must precede the listener rule at %d", last, catchAll)
	}
	if cfg["route"].(map[string]any)["default_domain_resolver"].(map[string]any)["strategy"] != "prefer_ipv6" {
		t.Fatal("IPv6 preference ignored")
	}
	ds.IPv4Only = true
	cfg, err = d.BuildResourceConfig(ds, nodes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["route"].(map[string]any)["default_domain_resolver"].(map[string]any)["strategy"] != "ipv4_only" {
		t.Fatal("an IPv4-only host cannot prefer IPv6")
	}
	if out := os.Getenv("CTLVPS_DUMP_MEMBER_CONFIG"); out != "" {
		ds.IPv4Only = false
		cfg, _ = d.BuildResourceConfig(ds, nodes, nil)
		data, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(out, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMembersNeedSharedListener(t *testing.T) {
	dir := t.TempDir()
	srv := domain.Server{ID: 1, Name: "la", PublicHost: "203.0.113.10", CoreMode: domain.CoreModeStable, CertMode: "self_signed"}
	ds := &agentproto.DesiredState{ServerID: 1, PublicHost: "203.0.113.10", Tuning: agentproto.Tuning{GoMemLimitMB: 128}}
	d := NewSingBox(Paths{BinDir: dir, ConfDir: dir, LogDir: dir, CertDir: filepath.Join(dir, "certs"), DataDir: dir}, NewSystemd())
	nodes := []agentproto.NodeSpec{specFor(t, srv, 5, "trojan", 443, false), memberSpec(20, 5, "11111111-1111-4111-8111-111111111111")}
	if _, err := d.BuildResourceConfig(ds, nodes, nil); err == nil {
		t.Fatal("member accepted on a listener that cannot tell users apart")
	}
}
