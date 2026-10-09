package core

import (
	"path/filepath"
	"strings"
	"testing"

	"ctlvps/internal/agentproto"
	"ctlvps/internal/domain"
)

// An entry reaches a Hysteria2 landing over one QUIC connection per user and
// accepts no certificate but the one the panel issued for that inbound.
func TestHysteria2Landing(t *testing.T) {
	dir := t.TempDir()
	d := NewSingBox(Paths{BinDir: dir, ConfDir: dir, LogDir: dir, CertDir: filepath.Join(dir, "certs"), DataDir: dir}, NewSystemd())

	landingSrv := domain.Server{ID: 2, Name: "home", PublicHost: "203.0.113.77", CoreMode: domain.CoreModeStable, CertMode: "self_signed"}
	landing := specFor(t, landingSrv, 30, "hysteria2", 26905, false)
	cert, _ := landing.Params["tls_cert"].(string)
	if len(cert) < 200 || strings.ContainsAny(cert, "\r\n") || landing.Params["tls_key"] == nil {
		t.Fatalf("a self-signed Hysteria2 inbound gets its certificate from the panel: %v", landing.Params)
	}
	lds := &agentproto.DesiredState{ServerID: 2, PublicHost: "203.0.113.77", Tuning: agentproto.Tuning{GoMemLimitMB: 128}}
	cfg, err := d.BuildResourceConfig(lds, []agentproto.NodeSpec{landing}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tls := cfg["inbounds"].([]any)[0].(map[string]any)["tls"].(map[string]any)
	if got := pemBody(tls["certificate"].([]string)); got != cert || tls["key"] == nil || tls["certificate_path"] != nil {
		t.Fatalf("the landing serves the issued certificate, not one of its own: %v", tls)
	}

	entrySrv := domain.Server{ID: 1, Name: "la", PublicHost: "203.0.113.10", CoreMode: domain.CoreModeStable, CertMode: "self_signed"}
	parent := specFor(t, entrySrv, 5, "vless", 443, false)
	member := memberSpec(20, 5, "11111111-1111-4111-8111-111111111111")
	member.Relay = &agentproto.RelaySpec{Protocol: "hysteria2", Server: "203.0.113.77", Port: 26905, Password: "member-password-0123456789", ServerName: "203.0.113.77", Cert: cert}
	if err := member.Relay.Validate(); err != nil {
		t.Fatal(err)
	}
	eds := &agentproto.DesiredState{ServerID: 1, PublicHost: "203.0.113.10", Tuning: agentproto.Tuning{GoMemLimitMB: 128}, Nodes: []agentproto.NodeSpec{parent, member}, Revision: 1}
	eds.Hash = agentproto.ContentHash(eds)
	if err := agentproto.ValidateDesired(eds, 1, 0, ""); err != nil {
		t.Fatal("an entry agent would refuse a Hysteria2 landing:", err)
	}
	cfg, err = d.BuildResourceConfig(eds, eds.Nodes, nil)
	if err != nil {
		t.Fatal(err)
	}
	var relay map[string]any
	for _, o := range cfg["outbounds"].([]any) {
		if m := o.(map[string]any); m["tag"] == "node-20-relay" {
			relay = m
		}
	}
	rtls, _ := relay["tls"].(map[string]any)
	if relay["type"] != "hysteria2" || relay["server"] != "203.0.113.77" || relay["server_port"] != 26905 || relay["password"] != "member-password-0123456789" || relay["routing_mark"] == nil ||
		rtls["server_name"] != "203.0.113.77" || rtls["insecure"] != nil || pemBody(rtls["certificate"].([]string)) != cert {
		t.Fatalf("relay outbound: %v", relay)
	}

	for name, bad := range map[string]agentproto.RelaySpec{
		"short password":      {Protocol: "hysteria2", Server: "203.0.113.77", Port: 26905, Password: "short", ServerName: "203.0.113.77", Cert: cert},
		"not a cert":          {Protocol: "hysteria2", Server: "203.0.113.77", Port: 26905, Password: "member-password-0123456789", ServerName: "203.0.113.77", Cert: "AAAA"},
		"two certs":           {Protocol: "hysteria2", Server: "203.0.113.77", Port: 26905, Password: "member-password-0123456789", ServerName: "203.0.113.77", Cert: cert + cert},
		"no server name":      {Protocol: "hysteria2", Server: "203.0.113.77", Port: 26905, Password: "member-password-0123456789", Cert: cert},
		"reality leftovers":   {Protocol: "hysteria2", Server: "203.0.113.77", Port: 26905, Password: "member-password-0123456789", ServerName: "203.0.113.77", Cert: cert, UUID: "11111111-1111-4111-8111-111111111111"},
		"cert on shadowsocks": {Protocol: "ss", Server: "203.0.113.77", Port: 26905, Method: "2022-blake3-aes-128-gcm", Password: "AAAAAAAAAAAAAAAAAAAAAA==:AAAAAAAAAAAAAAAAAAAAAA==", Cert: cert},
	} {
		if bad.Validate() == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

// pemBody returns the base64 between the PEM guard lines.
func pemBody(lines []string) string {
	return strings.Join(lines[1:len(lines)-1], "")
}
