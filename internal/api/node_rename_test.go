package api

import (
	"context"
	"strings"
	"testing"

	"ctlvps/internal/domain"
)

func TestExternalNodesCanBeRenamed(t *testing.T) {
	c := newTestAPI(t)
	c.do("POST", "/api/v1/auth/setup", map[string]any{"setup_token": testSetupToken, "username": "admin", "password": "password123"}, 200)
	// A pasted link.
	imp := c.do("POST", "/api/v1/nodes/import", map[string]any{"text": "ss://YWVzLTI1Ni1nY206cGFzcw==@1.2.3.4:8388#@someone-x1y2"}, 200)
	manual := imp["created"].([]any)[0].(map[string]any)
	got := c.do("PUT", "/api/v1/nodes/"+itoa(manual["id"]), map[string]any{"name": "朋友的线路"}, 200)
	if got["name"] != "朋友的线路" || got["server"] != "1.2.3.4" || got["port"] != float64(8388) {
		t.Fatalf("renamed link: %v", got)
	}
	c.do("PUT", "/api/v1/nodes/"+itoa(manual["id"]), map[string]any{"name": " "}, 400)

	// A node of an external subscription: only its name is ours to set.
	ctx := context.Background()
	ext := &domain.ExternalSubscription{Name: "airport", URL: "https://feed.example/sub", Enabled: true}
	if err := c.api.Store.CreateExternal(ctx, ext); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := c.api.Store.ReplaceExternalNodes(ctx, ext.ID, []domain.Node{{Name: "HK 01", Protocol: "ss", Server: "hk.example", Port: 443}}); err != nil {
		t.Fatal(err)
	}
	nodes := c.do("GET", "/api/v1/nodes?external_sub_id="+itoa(float64(ext.ID)), nil, 200)["list"].([]any)
	id := itoa(nodes[0].(map[string]any)["id"])
	got = c.do("PUT", "/api/v1/nodes/"+id, map[string]any{"name": "香港备用", "server": "evil.example", "port": 1}, 200)
	if got["name"] != "香港备用" || got["upstream_name"] != "HK 01" || got["server"] != "hk.example" || got["port"] != float64(443) {
		t.Fatalf("renamed subscription node: %v", got)
	}
	c.do("PUT", "/api/v1/nodes/"+id, map[string]any{"name": ""}, 400)
	c.do("PUT", "/api/v1/nodes/"+id, map[string]any{"name": strings.Repeat("长", 65)}, 400)
	c.do("PUT", "/api/v1/nodes/"+id, map[string]any{"name": "两\n行"}, 400)
}
