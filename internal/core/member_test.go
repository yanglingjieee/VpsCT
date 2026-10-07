package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ctlvps/internal/agentproto"
	"ctlvps/internal/domain"
	"ctlvps/internal/provision"
)

const (
	relayUUID = "44444444-4444-4444-8444-444444444444"
	relayKey  = "Zm9vYmFyZm9vYmFyZm9vYmFyZm9vYmFyZm9vYmFyZm8"
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
	relayed := memberSpec(20, 5, "11111111-1111-4111-8111-111111111111")
	relayed.Relay = &agentproto.RelaySpec{Server: "203.0.113.77", Port: 26903, UUID: relayUUID, ServerName: "www.example.com", PublicKey: relayKey, ShortID: "0123abcd"}
	if err := relayed.Relay.Validate(); err != nil {
		t.Fatal(err)
	}
	nodes := []agentproto.NodeSpec{
		memberSpec(21, 5, "22222222-2222-4222-8222-222222222222"), blocked, parent, other, relayed,
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
	if len(tags) != 4 || tags["node-20-relay"] == tags["node-21-direct"] || tags["node-20-relay"] == tags["node-5-direct"] || tags["node-20-relay"] == nil {
		t.Fatalf("every credential needs its own accounting mark: %v", tags)
	}
	for _, o := range cfg["outbounds"].([]any) {
		m := o.(map[string]any)
		if m["tag"] != "node-20-relay" {
			continue
		}
		reality := m["tls"].(map[string]any)["reality"].(map[string]any)
		if m["type"] != "vless" || m["server"] != "203.0.113.77" || m["server_port"] != 26903 || m["uuid"] != relayUUID || reality["public_key"] != relayKey {
			t.Fatalf("a relay member leaves through its landing with the user's credential there: %v", m)
		}
	}
	for _, r := range cfg["route"].(map[string]any)["rules"].([]any) {
		m := r.(map[string]any)
		if u, ok := m["auth_user"].([]string); ok && u[0] == "n20" && m["outbound"] != "node-20-relay" {
			t.Fatalf("relay member routed elsewhere: %v", m)
		}
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

func TestOptimisticDNSOnModernCores(t *testing.T) {
	dir := t.TempDir()
	srv := domain.Server{ID: 1, Name: "la", PublicHost: "203.0.113.10", CoreMode: domain.CoreModeStable, CertMode: "self_signed"}
	d := NewSingBox(Paths{BinDir: dir, ConfDir: dir, LogDir: dir, CertDir: filepath.Join(dir, "certs"), DataDir: dir}, NewSystemd())
	for version, want := range map[string]bool{"1.14.1": true, "1.13.4": false, "": false} {
		ds := &agentproto.DesiredState{ServerID: 1, PublicHost: "203.0.113.10", Tuning: agentproto.Tuning{GoMemLimitMB: 128},
			Versions: map[string]agentproto.CoreVersion{"sing-box": {Version: version}}}
		cfg, err := d.BuildConfig(ds, []agentproto.NodeSpec{specFor(t, srv, 5, "vless", 443, false)})
		if err != nil {
			t.Fatal(err)
		}
		if _, got := cfg["dns"].(map[string]any)["optimistic"]; got != want {
			t.Fatalf("sing-box %q: optimistic DNS cache = %v, want %v", version, got, want)
		}
	}
}

func TestMembersOnEveryShareableProtocol(t *testing.T) {
	dir := t.TempDir()
	srv := domain.Server{ID: 1, Name: "la", PublicHost: "203.0.113.10", CoreMode: domain.CoreModeStable, CertMode: "self_signed"}
	ds := &agentproto.DesiredState{ServerID: 1, PublicHost: "203.0.113.10", Tuning: agentproto.Tuning{GoMemLimitMB: 128}}
	uuid, pw := "55555555-5555-4555-8555-555555555555", "member-password-0123456789"
	key := provision.SS2022Key(16)
	creds := map[string]map[string]any{
		"vless": {"uuid": uuid, "flow": "xtls-rprx-vision"}, "trojan": {"password": pw}, "anytls": {"password": pw},
		"hysteria2": {"password": pw}, "tuic": {"uuid": uuid, "password": pw}, "ss": {"password": key},
	}
	var nodes []agentproto.NodeSpec
	id := int64(10)
	parents := map[string]int64{}
	for _, proto := range []string{"vless", "trojan", "anytls", "hysteria2", "tuic", "ss"} {
		id++
		parents[proto] = id
		nodes = append(nodes, specFor(t, srv, id, proto, 20000+int(id), false))
		nodes = append(nodes, agentproto.NodeSpec{NodeID: id + 100, Protocol: proto, Core: "singbox", AttachTo: id, Params: creds[proto]})
	}
	// One user of the trojan listener leaves through a Shadowsocks landing.
	relayed := agentproto.NodeSpec{NodeID: 200, Protocol: "trojan", Core: "singbox", AttachTo: parents["trojan"], Params: map[string]any{"password": pw + "x"},
		Relay: &agentproto.RelaySpec{Protocol: "ss", Server: "203.0.113.77", Port: 26903, Method: "2022-blake3-aes-128-gcm", Password: provision.SS2022Key(16) + ":" + key}}
	if err := relayed.Relay.Validate(); err != nil {
		t.Fatal(err)
	}
	nodes = append(nodes, relayed)
	ds.Nodes = nodes
	ds.Revision, ds.Hash = 1, ""
	ds.Hash = agentproto.ContentHash(ds)
	if err := agentproto.ValidateDesired(ds, 1, 0, ""); err != nil {
		t.Fatal("agents would refuse members on these protocols:", err)
	}
	d := NewSingBox(Paths{BinDir: dir, ConfDir: dir, LogDir: dir, CertDir: filepath.Join(dir, "certs"), DataDir: dir}, NewSystemd())
	cfg, err := d.BuildResourceConfig(ds, nodes, nil)
	if err != nil {
		t.Fatal(err)
	}
	byTag := map[string]map[string]any{}
	for _, in := range cfg["inbounds"].([]any) {
		m := in.(map[string]any)
		byTag[m["tag"].(string)] = m
	}
	for proto, pid := range parents {
		in := byTag[InboundTag(pid)]
		users, _ := in["users"].([]any)
		var names []string
		for _, u := range users {
			names = append(names, u.(map[string]any)["name"].(string))
		}
		want := []string{MemberUser(pid), MemberUser(pid + 100)}
		if proto == "ss" {
			want = want[1:] // the server key alone no longer authenticates
		}
		if proto == "trojan" {
			want = append(want, MemberUser(200))
		}
		if got, _ := json.Marshal(names); string(got) != string(mustJSON(want)) {
			t.Fatalf("%s users: %s, want %s", proto, got, mustJSON(want))
		}
	}
	routed := map[string]string{}
	for _, r := range cfg["route"].(map[string]any)["rules"].([]any) {
		m := r.(map[string]any)
		if u, ok := m["auth_user"].([]string); ok && m["action"] == "route" {
			routed[u[0]] = m["outbound"].(string)
		}
	}
	if len(routed) != 7 || routed["n200"] != "node-200-relay" {
		t.Fatalf("every member needs its own route: %v", routed)
	}
	for _, o := range cfg["outbounds"].([]any) {
		if m := o.(map[string]any); m["tag"] == "node-200-relay" && (m["type"] != "shadowsocks" || m["method"] != "2022-blake3-aes-128-gcm" || m["routing_mark"] == nil) {
			t.Fatalf("shadowsocks relay: %v", m)
		}
	}
	if out := os.Getenv("CTLVPS_DUMP_PROTOCOL_CONFIG"); out != "" {
		data, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(out, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
