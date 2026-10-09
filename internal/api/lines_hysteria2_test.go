package api

import (
	"context"
	"testing"

	"ctlvps/internal/agentproto"
)

// A landing whose provider only carries long-lived flows well is reached
// over Hysteria2: the entry is told the user's password there and the one
// certificate to accept.
func TestRelayToHysteria2Landing(t *testing.T) {
	c := newTestAPI(t)
	c.do("POST", "/api/v1/auth/setup", map[string]any{"setup_token": testSetupToken, "username": "admin", "password": "password123"}, 200)
	entry := c.do("POST", "/api/v1/servers", map[string]any{"name": "entry", "public_host": "198.51.100.1"}, 201)
	landing := c.do("POST", "/api/v1/servers", map[string]any{"name": "home", "public_host": "203.0.113.9"}, 201)
	in := c.do("POST", "/api/v1/servers/"+itoa(entry["id"])+"/nodes", map[string]any{"name": "入口", "protocol": "vless", "port": 443, "sni": "mirrors.example.edu"}, 201)
	out := c.do("POST", "/api/v1/servers/"+itoa(landing["id"])+"/nodes", map[string]any{"name": "落地", "protocol": "hysteria2", "port": 26905}, 201)
	tuic := c.do("POST", "/api/v1/servers/"+itoa(landing["id"])+"/nodes", map[string]any{"name": "tuic", "protocol": "tuic", "port": 26906}, 201)

	landings := map[string]bool{}
	for _, n := range c.do("GET", "/api/v1/lines/candidates", nil, 200)["list"].([]any) {
		landings[n.(map[string]any)["name"].(string)] = n.(map[string]any)["landing"].(bool)
	}
	if !landings["落地"] || landings["tuic"] || !landings["入口"] {
		t.Fatalf("which inbounds an entry can relay to: %v", landings)
	}
	c.do("POST", "/api/v1/lines", map[string]any{"name": "坏", "entry_node_id": in["id"], "landing_node_id": tuic["id"]}, 400)
	line := c.do("POST", "/api/v1/lines", map[string]any{"name": "家宽", "entry_node_id": in["id"], "landing_node_id": out["id"]}, 201)
	if line["landing_server"] != "home" {
		t.Fatalf("line: %v", line)
	}
	c.do("POST", "/api/v1/shares", map[string]any{"name": "YANG", "line_mode": "all"}, 201)

	build := func(id any) *agentproto.DesiredState {
		srv, err := c.api.Store.GetServer(context.Background(), int64(id.(float64)))
		if err != nil {
			t.Fatal(err)
		}
		ds, err := c.api.Desired.Build(context.Background(), srv)
		if err != nil {
			t.Fatal(err)
		}
		ds.Revision = 1
		ds.Hash = agentproto.ContentHash(ds)
		if err := agentproto.ValidateDesired(ds, srv.ID, 0, ""); err != nil {
			t.Fatalf("%s's agent would refuse its state: %v", srv.Name, err)
		}
		return ds
	}
	var cert, password string
	for _, n := range build(landing["id"]).Nodes {
		switch {
		case n.NodeID == int64(out["id"].(float64)):
			cert, _ = n.Params["tls_cert"].(string)
			if n.Params["tls_key"] == nil || n.Cert == nil || n.Cert.Mode != "self_signed" {
				t.Fatalf("the landing is sent its certificate and key: %v", n.Params)
			}
		case n.AttachTo == int64(out["id"].(float64)):
			password, _ = n.Params["password"].(string)
		}
	}
	if cert == "" || len(password) < 16 {
		t.Fatalf("landing state lacks the certificate or the user's credential: cert=%d password=%d", len(cert), len(password))
	}
	var relay *agentproto.RelaySpec
	for _, n := range build(entry["id"]).Nodes {
		if n.Relay != nil {
			relay = n.Relay
		}
	}
	if relay == nil || relay.Protocol != "hysteria2" || relay.Server != "203.0.113.9" || relay.Port != 26905 || relay.Password != password || relay.Cert != cert || relay.ServerName != "203.0.113.9" {
		t.Fatalf("the entry relays with the user's own password and trusts the issued certificate only: %+v", relay)
	}
}
