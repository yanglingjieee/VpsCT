package api

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Shadowrocket and Surge name a profile after the last segment of its
// address, so a user's address has a form that ends in the name to carry.
func TestProfileAddressCanCarryItsName(t *testing.T) {
	c := newTestAPI(t)
	c.do("POST", "/api/v1/auth/setup", map[string]any{"setup_token": testSetupToken, "username": "admin", "password": "password123"}, 200)
	srv := c.do("POST", "/api/v1/servers", map[string]any{"name": "entry", "public_host": "198.51.100.1"}, 201)
	in := c.do("POST", "/api/v1/servers/"+itoa(srv["id"])+"/nodes", map[string]any{"name": "入口", "protocol": "vless", "port": 443, "sni": "mirrors.example.edu"}, 201)
	c.do("POST", "/api/v1/lines", map[string]any{"name": "直连", "entry_node_id": in["id"], "sort_order": 1}, 201)
	user := c.do("POST", "/api/v1/shares", map[string]any{"name": "YANG", "line_mode": "all"}, 201)
	sub := user["subscription"].(map[string]any)
	fetch := func(path string) (int, string, http.Header) {
		t.Helper()
		req, _ := http.NewRequest("GET", c.srv.URL+path, nil)
		req.Header.Set("User-Agent", "Shadowrocket/2615 CFNetwork/1568.200.51 Darwin/24.1.0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body), resp.Header
	}
	name := url.PathEscape("土豆饼的家")
	for _, base := range []string{"/s/" + sub["token"].(string), "/r/" + sub["short_code"].(string)} {
		status, body, header := fetch(base + "/shadowrocket/" + name)
		if status != 200 || !strings.Contains(body, "[Proxy]") || !strings.Contains(body, "直连") {
			t.Fatalf("%s: named Shadowrocket profile: %d %.120s", base[:3], status, body)
		}
		if !strings.Contains(header.Get("Content-Disposition"), ".conf") {
			t.Fatalf("%s: not served as a profile: %v", base[:3], header)
		}
		// The format in the address wins over what the client would get by default.
		if status, body, _ = fetch(base + "/mihomo/" + name); status != 200 || !strings.Contains(body, "proxies:") {
			t.Fatalf("%s: named Clash profile: %d %.120s", base[:3], status, body)
		}
	}
	if status, _, _ := fetch("/r/nosuchcode/shadowrocket/" + name); status != 404 {
		t.Fatalf("unknown code: %d", status)
	}
}
