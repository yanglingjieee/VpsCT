package api

import (
	"strings"
	"testing"
)

func TestLinesGiveEveryUserTheirOwnCredentials(t *testing.T) {
	c := newTestAPI(t)
	c.do("POST", "/api/v1/auth/setup", map[string]any{"setup_token": testSetupToken, "username": "admin", "password": "password123"}, 200)
	server := func(name, host string, extra map[string]any) map[string]any {
		body := map[string]any{"name": name, "public_host": host}
		for k, v := range extra {
			body[k] = v
		}
		return c.do("POST", "/api/v1/servers", body, 201)
	}
	entry := server("entry", "198.51.100.1", map[string]any{"ipv4_only": true})
	landing := server("landing", "203.0.113.9", map[string]any{"prefer_ipv6": true})
	if landing["prefer_ipv6"] != true {
		t.Fatalf("IPv6 preference not stored: %v", landing)
	}
	c.do("POST", "/api/v1/servers", map[string]any{"name": "bad", "public_host": "192.0.2.1", "ipv4_only": true, "prefer_ipv6": true}, 400)
	deploy := func(srv map[string]any, name string, port int, sni string) map[string]any {
		return c.do("POST", "/api/v1/servers/"+itoa(srv["id"])+"/nodes", map[string]any{"name": name, "protocol": "vless", "port": port, "sni": sni}, 201)
	}
	in := deploy(entry, "入口", 443, "mirrors.example.edu")
	out := deploy(landing, "落地", 26903, "www.example.com")
	trojan := c.do("POST", "/api/v1/servers/"+itoa(entry["id"])+"/nodes", map[string]any{"name": "trojan", "protocol": "trojan", "port": 8443}, 201)

	if got := c.do("GET", "/api/v1/lines/candidates", nil, 200)["list"].([]any); len(got) != 2 {
		t.Fatalf("only VLESS listeners can be shared: %v", got)
	}
	c.do("POST", "/api/v1/lines", map[string]any{"name": "坏", "entry_node_id": trojan["id"]}, 400)
	direct := c.do("POST", "/api/v1/lines", map[string]any{"name": "直连", "entry_node_id": in["id"], "sort_order": 1}, 201)
	relay := c.do("POST", "/api/v1/lines", map[string]any{"name": "家宽", "entry_node_id": in["id"], "landing_node_id": out["id"], "sort_order": 2}, 201)
	if relay["entry_server"] != "entry" || relay["landing_server"] != "landing" || direct["users"].(float64) != 0 {
		t.Fatalf("line view: %v", relay)
	}

	user := func(name string, body map[string]any) (map[string]any, string) {
		body["name"] = name
		sh := c.do("POST", "/api/v1/shares", body, 201)
		sub := sh["subscription"].(map[string]any)
		r := c.do("GET", "/api/v1/subscriptions/"+itoa(sub["id"])+"/render?format=mihomo", nil, 200)
		return sh, r["body"].(string)
	}
	yang, yangBody := user("YANG", map[string]any{"line_mode": "all", "quota_bytes": 1 << 30, "reset_day": 1})
	_, sanBody := user("sansan", map[string]any{"line_mode": "selected", "line_ids": []any{direct["id"]}})
	c.do("POST", "/api/v1/shares", map[string]any{"name": "x", "line_mode": "nonsense"}, 400)
	if !strings.Contains(yangBody, "name: 直连") || !strings.Contains(yangBody, "name: 家宽") || !strings.Contains(yangBody, "dialer-proxy: 直连") {
		t.Fatalf("YANG's lines:\n%s", yangBody)
	}
	if strings.Contains(sanBody, "家宽") || !strings.Contains(sanBody, "name: 直连") {
		t.Fatalf("sansan must only see the line chosen for her:\n%s", sanBody)
	}
	uuid := func(body, name string) string {
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, "name: "+name+",") {
				i := strings.Index(line, "uuid: ")
				return line[i+6 : i+42]
			}
		}
		t.Fatalf("no proxy %q in:\n%s", name, body)
		return ""
	}
	if uuid(yangBody, "直连") == uuid(sanBody, "直连") {
		t.Fatal("two users share one credential")
	}
	if len(yang["nodes"].([]any)) != 2 {
		t.Fatalf("YANG owns an entry and a landing credential: %v", yang["nodes"])
	}
	for _, n := range c.do("GET", "/api/v1/nodes", nil, 200)["list"].([]any) {
		if n.(map[string]any)["attach_node_id"] != nil {
			t.Fatal("user credentials must not clutter the node list")
		}
	}
	if lines := c.do("GET", "/api/v1/lines", nil, 200)["list"].([]any); lines[0].(map[string]any)["users"].(float64) != 2 || lines[1].(map[string]any)["users"].(float64) != 1 {
		t.Fatalf("users per line: %v", lines)
	}

	// The camouflage domain and port of a live listener change in place:
	// same keys, same user secrets, new address in everyone's subscription.
	before := uuid(yangBody, "直连")
	edited := c.do("PUT", "/api/v1/nodes/"+itoa(in["id"]), map[string]any{"sni": "Mirror.Example.org", "listen_port": 8443}, 409)
	_ = edited
	edited = c.do("PUT", "/api/v1/nodes/"+itoa(in["id"]), map[string]any{"sni": "Mirror.Example.org", "listen_port": 2053}, 200)
	if edited["listen_port"].(float64) != 2053 {
		t.Fatalf("port not moved: %v", edited)
	}
	c.do("PUT", "/api/v1/nodes/"+itoa(in["id"]), map[string]any{"sni": "not a host"}, 400)
	sub := yang["subscription"].(map[string]any)
	yangBody = c.do("GET", "/api/v1/subscriptions/"+itoa(sub["id"])+"/render?format=mihomo", nil, 200)["body"].(string)
	if uuid(yangBody, "直连") != before || !strings.Contains(yangBody, "servername: mirror.example.org") || !strings.Contains(yangBody, "port: 2053") || strings.Contains(yangBody, "mirrors.example.edu") {
		t.Fatalf("listener edit did not reach the subscription:\n%s", yangBody)
	}
	member := yang["nodes"].([]any)[0].(map[string]any)
	c.do("PUT", "/api/v1/nodes/"+itoa(member["id"]), map[string]any{"name": "x"}, 400)
	c.do("POST", "/api/v1/nodes/"+itoa(member["id"])+"/regenerate", nil, 400)

	// Each client format gets its own default template through one link.
	tpl := c.do("POST", "/api/v1/templates", map[string]any{"name": "sr", "kind": "shadowrocket", "content": "[Proxy]\n{{PROXIES}}\n[Proxy Group]\nPROXY = select,{{all}}\n[Rule]\nFINAL,PROXY\n"}, 201)
	c.do("PUT", "/api/v1/settings", map[string]any{"subscription.template.shadowrocket": itoa(tpl["id"])}, 200)
	sr := c.do("GET", "/api/v1/subscriptions/"+itoa(sub["id"])+"/render?format=shadowrocket", nil, 200)["body"].(string)
	if !strings.Contains(sr, "PROXY = select,直连,家宽") || !strings.Contains(sr, "underlying-proxy=") {
		t.Fatalf("shadowrocket default template not used:\n%s", sr)
	}

	// Removing a line takes the credentials that only it needed.
	c.do("DELETE", "/api/v1/lines/"+itoa(relay["id"]), nil, 204)
	if got := c.do("GET", "/api/v1/shares/"+itoa(yang["id"]), nil, 200); len(got["nodes"].([]any)) != 1 {
		t.Fatalf("landing credential left behind: %v", got["nodes"])
	}
}
