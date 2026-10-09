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

	snell := c.do("POST", "/api/v1/servers/"+itoa(entry["id"])+"/nodes", map[string]any{"name": "snell", "protocol": "snell", "port": 9443}, 201)
	got := c.do("GET", "/api/v1/lines/candidates", nil, 200)["list"].([]any)
	if len(got) != 3 || got[2].(map[string]any)["protocol"] != "trojan" || got[2].(map[string]any)["landing"] != false || got[0].(map[string]any)["landing"] != true {
		t.Fatalf("every multi-user inbound can be an entry, only some a landing: %v", got)
	}
	// One port, one identity: such an inbound cannot serve several users.
	c.do("POST", "/api/v1/lines", map[string]any{"name": "坏", "entry_node_id": snell["id"]}, 400)
	// A relay cannot verify a landing that depends on a certificate.
	c.do("POST", "/api/v1/lines", map[string]any{"name": "坏", "entry_node_id": in["id"], "landing_node_id": trojan["id"]}, 400)
	direct := c.do("POST", "/api/v1/lines", map[string]any{"name": "直连", "entry_node_id": in["id"], "sort_order": 1}, 201)
	relay := c.do("POST", "/api/v1/lines", map[string]any{"name": "家宽", "entry_node_id": in["id"], "landing_node_id": out["id"], "sort_order": 2}, 201)
	if relay["entry_server"] != "entry" || relay["landing_server"] != "landing" || direct["users"].(float64) != 0 {
		t.Fatalf("line view: %v", relay)
	}

	render := func(sh map[string]any, format string) string {
		sub := sh["subscription"].(map[string]any)
		return c.do("GET", "/api/v1/subscriptions/"+itoa(sub["id"])+"/render?format="+format, nil, 200)["body"].(string)
	}
	user := func(name string, body map[string]any) (map[string]any, string) {
		body["name"] = name
		sh := c.do("POST", "/api/v1/shares", body, 201)
		return sh, render(sh, "mihomo")
	}
	yang, yangBody := user("YANG", map[string]any{"line_mode": "all", "quota_bytes": 1 << 30, "reset_day": 1})
	san, sanBody := user("sansan", map[string]any{"line_mode": "selected", "line_ids": []any{direct["id"]}})
	c.do("POST", "/api/v1/shares", map[string]any{"name": "x", "line_mode": "nonsense"}, 400)
	c.do("POST", "/api/v1/shares", map[string]any{"name": "x", "delivery": "nonsense"}, 400)
	// Every line, relays included, is one ordinary proxy on the entry server:
	// the landing never appears in a client profile.
	if !strings.Contains(yangBody, "name: 直连") || !strings.Contains(yangBody, "name: 家宽") || strings.Contains(yangBody, "dialer-proxy") || strings.Contains(yangBody, "203.0.113.9") || strings.Contains(yangBody, "26903") {
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
	if uuid(yangBody, "直连") == uuid(sanBody, "直连") || uuid(yangBody, "直连") == uuid(yangBody, "家宽") {
		t.Fatal("every user and every line has its own credential")
	}
	if len(yang["nodes"].([]any)) != 3 {
		t.Fatalf("YANG owns a credential per line and machine: %v", yang["nodes"])
	}
	usage := yang["lines"].([]any)
	if len(usage) != 2 || usage[1].(map[string]any)["landing_server"] != "landing" || usage[1].(map[string]any)["entry_server"] != "entry" || usage[1].(map[string]any)["ready"] != true {
		t.Fatalf("per-line usage on both machines: %v", usage)
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
	// same keys, same user secrets, new address in everyone's profile.
	before := uuid(yangBody, "直连")
	c.do("PUT", "/api/v1/nodes/"+itoa(in["id"]), map[string]any{"sni": "Mirror.Example.org", "listen_port": 8443}, 409)
	edited := c.do("PUT", "/api/v1/nodes/"+itoa(in["id"]), map[string]any{"sni": "Mirror.Example.org", "listen_port": 2053}, 200)
	if edited["listen_port"].(float64) != 2053 {
		t.Fatalf("port not moved: %v", edited)
	}
	c.do("PUT", "/api/v1/nodes/"+itoa(in["id"]), map[string]any{"sni": "not a host"}, 400)
	yangBody = render(yang, "mihomo")
	if uuid(yangBody, "直连") != before || strings.Count(yangBody, "servername: mirror.example.org") != 2 || strings.Count(yangBody, "port: 2053") != 2 || strings.Contains(yangBody, "mirrors.example.edu") {
		t.Fatalf("listener edit did not reach the profile:\n%s", yangBody)
	}
	member := yang["nodes"].([]any)[0].(map[string]any)
	c.do("PUT", "/api/v1/nodes/"+itoa(member["id"]), map[string]any{"name": "x"}, 400)
	c.do("POST", "/api/v1/nodes/"+itoa(member["id"])+"/regenerate", nil, 400)

	// Rules: the panel has one list, for every user and every client. Until
	// it is written it is the built-in minimum, where everything but the
	// local network takes the chosen line.
	if !strings.Contains(yangBody, "name: 节点选择") || !strings.Contains(yangBody, "MATCH,节点选择") || !strings.Contains(yangBody, "IP-CIDR,192.168.0.0/16,DIRECT,no-resolve") {
		t.Fatalf("the built-in rules:\n%s", yangBody)
	}
	if got := c.do("GET", "/api/v1/rules", nil, 200); got["saved"] != false || got["group_name"] != "节点选择" || !strings.Contains(got["rules"].(string), "MATCH,PROXY") || len(got["lists"].([]any)) < 10 {
		t.Fatalf("the editor shows what is in effect before anything was saved: %v", got)
	}
	c.do("PUT", "/api/v1/rules", map[string]any{"rules": "DOMAIN-SUFFIX,cn\n"}, 400)
	c.do("PUT", "/api/v1/rules", map[string]any{"rules": "RULE-SET,nonesuch,DIRECT\n"}, 400)
	c.do("PUT", "/api/v1/rules", map[string]any{"rules": "MATCH,PROXY\n", "group_name": "DIRECT"}, 400)
	rules := c.do("PUT", "/api/v1/rules", map[string]any{"group_name": "我的线路",
		"rules": "# 国内直连\nDOMAIN-SUFFIX,cn,DIRECT\nRULE-SET,direct,DIRECT\nRULE-SET,cncidr,DIRECT,no-resolve\nRULE-SET,applications,DIRECT\nDOMAIN,ads.example,REJECT\nFINAL,PROXY\n"}, 200)
	if rules["saved"] != true || rules["group_name"] != "我的线路" || !strings.HasPrefix(rules["rules"].(string), "# 国内直连\n") {
		t.Fatalf("saved rules: %v", rules)
	}
	yang = c.do("GET", "/api/v1/shares/"+itoa(yang["id"]), nil, 200)
	if len(yang["formats"].([]any)) != 2 || yang["ruleset_name"] != nil {
		t.Fatalf("a user has a profile for both client families and no rule set of their own: %v %v", yang["formats"], yang["ruleset_name"])
	}
	// The same rules reach both client families, each in its own terms: a
	// selector of lines for Clash, the line tapped on the home page (PROXY)
	// for Shadowrocket, and the lists from where the panel serves them.
	sr := render(yang, "shadowrocket")
	for _, want := range []string{"直连 = vless, ", "家宽 = vless, ", "DOMAIN-SUFFIX,cn,DIRECT", "RULE-SET," + c.srv.URL + "/rules/surge/direct.list,DIRECT",
		"RULE-SET," + c.srv.URL + "/rules/surge/cncidr.list,DIRECT\n", "DOMAIN,ads.example,REJECT", "FINAL,PROXY"} {
		if !strings.Contains(sr, want) {
			t.Fatalf("shadowrocket lacks %q:\n%s", want, sr)
		}
	}
	if strings.Contains(sr, "[Proxy Group]") || strings.Contains(sr, "我的线路") || strings.Contains(sr, "applications") || strings.Contains(sr, "underlying-proxy") {
		t.Fatalf("shadowrocket: no group, no list only Clash can read, no chain:\n%s", sr)
	}
	clash := render(yang, "mihomo")
	for _, want := range []string{"name: 我的线路", "DOMAIN-SUFFIX,cn,DIRECT", "RULE-SET,direct,DIRECT", "RULE-SET,cncidr,DIRECT,no-resolve", "RULE-SET,applications,DIRECT", "MATCH,我的线路",
		"behavior: ipcidr", "behavior: classical", c.srv.URL + "/rules/clash/direct.yaml", "https://1.1.1.1/dns-query#我的线路", "rule-set:direct", "+.cn"} {
		if !strings.Contains(clash, want) {
			t.Fatalf("clash lacks %q:\n%s", want, clash)
		}
	}
	// Everybody has the same rules, whenever they were created.
	if sr := render(san, "shadowrocket"); !strings.Contains(sr, "直连 = vless, ") || !strings.Contains(sr, "DOMAIN,ads.example,REJECT") || strings.Contains(sr, "[Proxy Group]") {
		t.Fatalf("sansan's shadowrocket:\n%s", sr)
	}
	next := c.do("POST", "/api/v1/shares", map[string]any{"name": "新人", "line_mode": "all"}, 201)
	if body := render(next, "mihomo"); !strings.Contains(body, "name: 我的线路") || !strings.Contains(body, "DOMAIN,ads.example,REJECT") {
		t.Fatalf("a new user's profile:\n%s", body)
	}

	// A user's own link: their page in a browser, their profile in a client.
	token := yang["subscription"].(map[string]any)["token"].(string)
	page := c.do("GET", "/s/"+token+"?page=1", nil, 200)
	if page["name"] != "YANG" || page["rules"] != nil || len(page["lines"].([]any)) != 2 || len(page["formats"].([]any)) != 2 || !strings.HasSuffix(page["url"].(string), "/s/"+token) || page["nodes"] != nil {
		t.Fatalf("personal page: %v", page)
	}
	if l := page["lines"].([]any)[1].(map[string]any); l["name"] != "家宽" || l["entry_server"] != "" || l["landing_server"] != nil {
		t.Fatalf("the page must not reveal which machines carry a line: %v", l)
	}

	// Carpooling: the same lines handed over as plain nodes, no profile.
	pool := c.do("POST", "/api/v1/shares", map[string]any{"name": "拼车", "line_mode": "all", "delivery": "nodes"}, 201)
	links := pool["node_links"].([]any)
	if len(links) != 2 || !strings.HasPrefix(links[1].(map[string]any)["uri"].(string), "vless://") || !strings.Contains(links[1].(map[string]any)["uri"].(string), "@198.51.100.1:2053") || pool["formats"] != nil {
		t.Fatalf("a relay line is one node link on its entry: %v", links)
	}
	poolToken := pool["subscription"].(map[string]any)["token"].(string)
	if page = c.do("GET", "/s/"+poolToken+"?page=1", nil, 200); page["delivery"] != "nodes" || len(page["nodes"].([]any)) != 2 || len(page["formats"].([]any)) != 0 {
		t.Fatalf("carpool page: %v", page)
	}
	// Should their client ask for a profile after all, it carries the same
	// rules as everybody's.
	if sr := render(pool, "shadowrocket"); !strings.Contains(sr, "DOMAIN,ads.example,REJECT") || !strings.Contains(sr, "FINAL,PROXY") {
		t.Fatalf("a node-only user's profile:\n%s", sr)
	}

	// Removing a line takes its credentials on both machines.
	c.do("DELETE", "/api/v1/lines/"+itoa(relay["id"]), nil, 204)
	if got := c.do("GET", "/api/v1/shares/"+itoa(yang["id"]), nil, 200); len(got["nodes"].([]any)) != 1 || len(got["lines"].([]any)) != 1 {
		t.Fatalf("relay credentials left behind: %v", got["nodes"])
	}
	// Emptying the rules brings the built-in minimum back, under the name
	// the selector was given.
	c.do("PUT", "/api/v1/rules", map[string]any{"rules": "", "group_name": "我的线路"}, 200)
	if body := render(yang, "mihomo"); !strings.Contains(body, "MATCH,我的线路") || !strings.Contains(body, "IP-CIDR,192.168.0.0/16,DIRECT,no-resolve") || strings.Contains(body, "ads.example") {
		t.Fatalf("emptied rules:\n%s", body)
	}
}
