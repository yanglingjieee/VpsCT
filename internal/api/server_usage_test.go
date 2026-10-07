package api

import (
	"strings"
	"testing"
	"time"

	"ctlvps/internal/agentproto"
)

func TestServerQuotaFollowsItsHost(t *testing.T) {
	c := newTestAPI(t)
	c.do("POST", "/api/v1/auth/setup", map[string]any{"setup_token": testSetupToken, "username": "admin", "password": "password123"}, 200)
	const gb = 1 << 30
	body := map[string]any{"name": "hk", "quota_bytes": 220 * gb, "quota_reset_day": 1, "quota_billing": "out"}
	srv := c.do("POST", "/api/v1/servers", body, 201)
	path := "/api/v1/servers/" + itoa(srv["id"])
	usage := func(v map[string]any) map[string]any { return v["usage"].(map[string]any) }
	if srv["quota_billing"] != "out" || usage(srv)["billing"] != "out" || usage(srv)["next_reset"] == nil {
		t.Fatalf("outbound-only quota not stored: %v", srv)
	}
	c.do("POST", "/api/v1/servers", map[string]any{"name": "bad", "quota_billing": "sideways"}, 400)

	// The host says 31 GB are gone; the panel has only just started counting.
	c.do("PUT", path+"/usage", map[string]any{}, 400)
	c.do("PUT", path+"/usage", map[string]any{"used_bytes": -1}, 400)
	got := usage(c.do("PUT", path+"/usage", map[string]any{"used_bytes": 31 * gb}, 200))
	if got["billed"] != float64(31*gb) || got["adjust"] != float64(31*gb) || got["measured"] != float64(0) {
		t.Fatalf("correction not applied: %v", got)
	}
	if got = usage(c.do("DELETE", path+"/usage", nil, 200)); got["billed"] != float64(0) || got["adjust"] != float64(0) {
		t.Fatalf("correction not removed: %v", got)
	}

	// Counted another way, or over another period, the figure means nothing.
	c.do("PUT", path+"/usage", map[string]any{"used_bytes": 31 * gb}, 200)
	body["quota_billing"] = "dual"
	if got = usage(c.do("PUT", path, body, 200)); got["adjust"] != float64(0) {
		t.Fatalf("correction survived a billing change: %v", got)
	}
	c.do("PUT", path+"/usage", map[string]any{"used_bytes": 31 * gb}, 200)
	body["quota_reset_day"] = 28
	if got = usage(c.do("PUT", path, body, 200)); got["adjust"] != float64(0) {
		t.Fatalf("correction survived a new reset day: %v", got)
	}
	body["quota_reset_day"] = 0
	if got = usage(c.do("PUT", path, body, 200)); got["next_reset"] != nil {
		t.Fatalf("rolling window has a reset: %v", got)
	}
	c.do("PUT", path+"/usage", map[string]any{"used_bytes": gb}, 400)
}

func TestResetTimezoneSetting(t *testing.T) {
	c := newTestAPI(t)
	c.do("POST", "/api/v1/auth/setup", map[string]any{"setup_token": testSetupToken, "username": "admin", "password": "password123"}, 200)
	if got := c.do("GET", "/api/v1/settings", nil, 200)["quota.timezone"]; got != "UTC" {
		t.Fatalf("default reset timezone = %v", got)
	}
	c.do("PUT", "/api/v1/settings", map[string]any{"quota.timezone": "Mars/Olympus"}, 400)
	c.do("PUT", "/api/v1/settings", map[string]any{"quota.timezone": "Asia/Shanghai"}, 200)
	srv := c.do("POST", "/api/v1/servers", map[string]any{"name": "vmiss", "quota_reset_day": 28}, 201)
	start := srv["usage"].(map[string]any)["period_start"].(string)
	if !strings.HasSuffix(start, "-28T00:00:00+08:00") {
		t.Fatalf("period does not start at midnight in Shanghai: %s", start)
	}
	if got := c.do("PUT", "/api/v1/settings", map[string]any{"quota.timezone": ""}, 200)["quota.timezone"]; got != "UTC" {
		t.Fatalf("empty reset timezone = %v", got)
	}
}

func TestServerStopsWhileItsQuotaIsUsedUp(t *testing.T) {
	c := newTestAPI(t)
	c.do("POST", "/api/v1/auth/setup", map[string]any{"setup_token": testSetupToken, "username": "admin", "password": "password123"}, 200)
	body := map[string]any{"name": "hk", "quota_bytes": 1000, "quota_reset_day": 1, "quota_billing": "out", "quota_stop": true}
	srv := c.do("POST", "/api/v1/servers", body, 201)
	path := "/api/v1/servers/" + itoa(srv["id"])
	c.do("POST", path+"/nodes", map[string]any{"protocol": "vless", "name": "hk-443", "port": 443, "sni": "www.example.com"}, 201)
	et := c.do("POST", path+"/enroll-token", nil, 200)
	admin := c.cookie
	c.cookie = nil
	c.agent = c.do("POST", "/api/agent/v1/enroll", agentproto.EnrollRequest{EnrollToken: et["token"].(string), Version: "test", Arch: "amd64"}, 200)["agent_token"].(string)
	carried := func(rx, tx int64) {
		t.Helper()
		c.do("POST", "/api/agent/v1/heartbeat", agentproto.Heartbeat{Epoch: "boot", TS: time.Now().UTC(), Metrics: agentproto.Metrics{NetRx: rx, NetTx: tx}}, 200)
	}
	inbounds := func() int {
		t.Helper()
		return len(c.do("GET", "/api/agent/v1/desired", nil, 200)["nodes"].([]any))
	}
	stopped := func() bool {
		t.Helper()
		saved := c.cookie
		c.cookie = admin
		defer func() { c.cookie = saved }()
		return c.do("GET", path, nil, 200)["quota_stopped"].(bool)
	}
	carried(1, 1)
	// Inbound traffic is not what this host bills.
	carried(50000, 901)
	if stopped() || inbounds() != 1 {
		t.Fatal("stopped under its quota")
	}
	carried(50000, 1001)
	if !stopped() || inbounds() != 0 {
		t.Fatal("still carrying traffic with its quota used up")
	}
	c.cookie = admin
	alerts := c.do("GET", "/api/v1/dashboard", nil, 200)["alerts"].([]any)
	if len(alerts) != 1 || alerts[0].(map[string]any)["kind"] != "quota_stopped" {
		t.Fatalf("overview does not say the server is stopped: %v", alerts)
	}
	// Saving the server does not lift the stop by itself.
	if got := c.do("PUT", path, body, 200); got["quota_stopped"] != true || got["enabled"] != true {
		t.Fatalf("stop lost on save: %v", got)
	}
	// Under the quota again, it runs again: here a correction, in life the reset.
	if got := c.do("PUT", path+"/usage", map[string]any{"used_bytes": 10}, 200); got["quota_stopped"] != false {
		t.Fatalf("not resumed: %v", got)
	}
	c.cookie = nil
	if inbounds() != 1 {
		t.Fatal("inbound not restored")
	}
	carried(50000, 2100)
	if !stopped() {
		t.Fatal("not stopped the second time")
	}
	// Alert only: the choice is the server's.
	c.cookie = admin
	body["quota_stop"] = false
	if got := c.do("PUT", path, body, 200); got["quota_stopped"] != false {
		t.Fatalf("still stopped after the choice was withdrawn: %v", got)
	}
	c.do("PUT", "/api/v1/settings", map[string]any{"quota.action": "stop"}, 400)
}
