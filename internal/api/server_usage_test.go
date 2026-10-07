package api

import (
	"strings"
	"testing"
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
