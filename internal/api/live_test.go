package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ctlvps/internal/agentproto"
	"ctlvps/internal/domain"
	"ctlvps/internal/live"
	"ctlvps/internal/liveproto"
	"ctlvps/internal/notify"
	"ctlvps/internal/report"
	"ctlvps/internal/store"

	"golang.org/x/net/websocket"
)

func (c *client) withLive(t *testing.T) *live.Hub {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	hub, err := live.New(ctx, c.api.Store, slog.New(slog.NewTextHandler(io.Discard, nil)), c.api.Events.Publish)
	if err != nil {
		t.Fatal(err)
	}
	c.api.Live = hub
	// The token is looked up for every message, and the servers slow down
	// as soon as nobody watches.
	hub.Revalidate, hub.Linger = 0, 0
	done := make(chan struct{})
	go func() { hub.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return hub
}

func (c *client) dialLive(t *testing.T, token string) (*websocket.Conn, error) {
	t.Helper()
	cfg, err := websocket.NewConfig("ws"+strings.TrimPrefix(c.srv.URL, "http")+liveproto.Path, c.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		cfg.Header.Set("Authorization", "Bearer "+token)
	}
	return websocket.DialConfig(cfg)
}

// The live channel end to end through the API: an agent's token opens it, a
// panel that is open makes the servers send every second and is handed each
// reading, and what was measured can be read back.
func TestLiveChannelReachesThePanel(t *testing.T) {
	c := newTestAPI(t)
	hub := c.withLive(t)
	c.do("POST", "/api/v1/auth/setup", map[string]any{"setup_token": testSetupToken, "username": "admin", "password": "password123"}, 200)
	admin := c.cookie
	srv := c.do("POST", "/api/v1/servers", map[string]any{"name": "搬瓦工", "price": 49.99, "currency": "$", "cycle": "year", "expires_at": "2026-11-29", "auto_renew": true}, 201)
	id := int64(srv["id"].(float64))
	if srv["price"] != 49.99 || srv["expires_at"] != "2026-11-29" || srv["auto_renew"] != true {
		t.Fatalf("what a server costs and when it runs out is kept: %v", srv)
	}
	c.do("PUT", "/api/v1/servers/"+itoa(srv["id"]), map[string]any{"name": "搬瓦工", "expires_at": "29/11/2026"}, 400)
	c.do("PUT", "/api/v1/servers/"+itoa(srv["id"]), map[string]any{"name": "搬瓦工", "auto_renew": true, "cycle": "once", "expires_at": "2026-11-29"}, 400)

	// Probe targets: public addresses only, each once.
	c.do("PUT", "/api/v1/probe-targets", map[string]any{"interval": 10, "targets": []map[string]any{{"name": "内网", "host": "192.168.1.1", "port": 80}}}, 400)
	c.do("PUT", "/api/v1/probe-targets", map[string]any{"interval": 2, "targets": []map[string]any{}}, 400)
	saved := c.do("PUT", "/api/v1/probe-targets", map[string]any{"interval": 10, "targets": []map[string]any{
		{"name": "四川电信", "host": "SC-CT-v4.ip.zstaticcdn.com", "port": 80, "carrier": "ct", "on_card": true},
		{"name": "四川联通", "host": "sc-cu-v4.ip.zstaticcdn.com", "port": 80, "carrier": "cu", "on_card": true},
	}}, 200)
	targets := saved["targets"].([]any)
	if len(targets) != 2 || targets[0].(map[string]any)["host"] != "sc-ct-v4.ip.zstaticcdn.com" {
		t.Fatalf("targets: %v", saved)
	}
	ct := int64(targets[0].(map[string]any)["id"].(float64))

	et := c.do("POST", "/api/v1/servers/"+itoa(srv["id"])+"/enroll-token", nil, 200)
	c.cookie = nil
	token := c.do("POST", "/api/agent/v1/enroll", agentproto.EnrollRequest{EnrollToken: et["token"].(string), Version: "test", Arch: "amd64"}, 200)["agent_token"].(string)

	if _, err := c.dialLive(t, ""); err == nil {
		t.Fatal("the live channel must want the agent's token")
	}
	ws, err := c.dialLive(t, token)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	read := func() liveproto.Down {
		t.Helper()
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		var raw string
		if err := websocket.Message.Receive(ws, &raw); err != nil {
			t.Fatal(err)
		}
		var d liveproto.Down
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	say := func(up liveproto.Up) {
		t.Helper()
		b, _ := json.Marshal(up)
		if err := websocket.Message.Send(ws, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	first := read()
	if first.IntervalMs != liveproto.IdleIntervalMs || first.Plan == nil || first.Plan.IntervalSec != 10 || len(first.Plan.Targets) != 2 || first.Plan.Validate() != nil {
		t.Fatalf("a worker is told how often to read and what to probe: %+v", first)
	}

	// The open channel does not take a place in the queue the agents'
	// requests wait in, and does not time out like a request.
	c.agent = token
	c.do("POST", "/api/agent/v1/heartbeat", agentproto.Heartbeat{Epoch: "boot", TS: time.Now(), Metrics: agentproto.Metrics{MemTotal: 1000}}, 200)

	// An admin opens the panel: the stream of events starts, the worker is
	// asked for a reading every second, and each reading is passed on.
	req, _ := http.NewRequest("GET", c.srv.URL+"/api/v1/events", nil)
	req.AddCookie(admin)
	stream, err := http.DefaultClient.Do(req)
	if err != nil || stream.StatusCode != 200 {
		t.Fatalf("events: %v %v", err, stream)
	}
	defer stream.Body.Close()
	if d := read(); d.IntervalMs != liveproto.WatchedIntervalMs {
		t.Fatalf("watched: %+v", d)
	}
	say(liveproto.Up{Host: &liveproto.Host{OS: "Debian GNU/Linux 12 (bookworm)", Virt: "kvm", Cores: 2, Arch: "amd64"}})
	say(liveproto.Up{Sample: &liveproto.Sample{CPU: 12.5, MemUsed: 600, MemTotal: 1000, RxRate: 2048, TxRate: 4096, TCP: 31, Load1: 0.4, Uptime: 86400}})
	say(liveproto.Up{Probes: []liveproto.ProbeResult{{Target: ct, RTT: 178_400}, {Target: ct, RTT: liveproto.Lost}, {Target: 999, RTT: 5}}})
	lines := bufio.NewScanner(stream.Body)
	var event live.Event
	for deadline := time.Now().Add(5 * time.Second); len(event.Samples) == 0 || len(event.Probes) == 0; {
		if time.Now().After(deadline) || !lines.Scan() {
			t.Fatalf("no live event reached the panel: %+v", event)
		}
		if lines.Text() != "event: live" || !lines.Scan() {
			continue
		}
		var e live.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines.Text(), "data: ")), &e); err != nil {
			t.Fatal(err)
		}
		if e.Samples != nil {
			event.Samples = e.Samples
		}
		event.Probes = append(event.Probes, e.Probes...)
	}
	if s := event.Samples[id]; s.CPU != 12.5 || s.TxRate != 4096 || s.TCP != 31 {
		t.Fatalf("the reading as the panel got it: %+v", event)
	}
	if len(event.Probes) != 2 || event.Probes[1].Minute.Sent != 2 || event.Probes[1].Minute.Lost != 1 || event.Probes[1].RTT != liveproto.Lost {
		t.Fatalf("probe results as the panel got them, without the target nobody asked for: %+v", event.Probes)
	}

	c.cookie, c.agent = admin, ""
	snap := c.do("GET", "/api/v1/live", nil, 200)
	view := snap["servers"].(map[string]any)[itoa(srv["id"])].(map[string]any)
	if view["connected"] != true || view["sample"].(map[string]any)["c"] != 12.5 || view["host"].(map[string]any)["virt"] != "kvm" {
		t.Fatalf("snapshot: %v", snap)
	}
	if got := c.do("GET", "/api/v1/servers/"+itoa(srv["id"]), nil, 200)["host"].(map[string]any); got["os"] != "Debian GNU/Linux 12 (bookworm)" || got["cores"] != float64(2) {
		t.Fatalf("a server is shown with what its host is: %v", got)
	}

	// What was measured can be read back once its minute is over.
	if err := c.api.Store.AddLiveMinutes(context.Background(),
		[]store.MetricPoint{{ServerID: id, TS: time.Now().Add(-5*time.Minute).Unix() / 60 * 60, N: 60, CPU: 20, CPUMax: 80, MemUsed: 600, MemTotal: 1000, RxRate: 1000, TxRate: 3000, RxMax: 9000, TxMax: 9000, TCP: 30}},
		[]store.ProbePoint{{ServerID: id, TargetID: ct, TS: time.Now().Add(-5*time.Minute).Unix() / 60 * 60, Sent: 6, Lost: 3, RTTSum: 540_000, RTTMin: 170_000, RTTMax: 190_000}}); err != nil {
		t.Fatal(err)
	}
	history := c.do("GET", "/api/v1/servers/"+itoa(srv["id"])+"/history?range=1h", nil, 200)
	if points := history["points"].([]any); len(points) != 1 || points[0].(map[string]any)["cpu_max"] != float64(80) {
		t.Fatalf("history: %v", history)
	}
	latency := c.do("GET", "/api/v1/servers/"+itoa(srv["id"])+"/latency?range=1h", nil, 200)["targets"].([]any)
	if first := latency[0].(map[string]any); first["name"] != "四川电信" || first["avg"] != float64(180_000) || first["lost"] != float64(3) || len(first["points"].([]any)) != 1 || len(latency[1].(map[string]any)["points"].([]any)) != 0 {
		t.Fatalf("latency: %v", latency)
	}
	c.do("GET", "/api/v1/servers/"+itoa(srv["id"])+"/history?range=1y", nil, 400)

	// The panel closes: with nobody watching, a reading every five seconds
	// is enough.
	stream.Body.Close()
	if d := read(); d.IntervalMs != liveproto.IdleIntervalMs {
		t.Fatalf("unwatched: %+v", d)
	}

	// A token that was reset no longer speaks for the server: the channel
	// ends at the next message.
	c.do("POST", "/api/v1/servers/"+itoa(srv["id"])+"/reset-token", nil, 204)
	say(liveproto.Up{Sample: &liveproto.Sample{CPU: 1}})
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var raw string
	for err = nil; err == nil; {
		err = websocket.Message.Receive(ws, &raw)
	}
	if view := hub.Snapshot().Servers[id]; view.Connected {
		t.Fatalf("a channel that ended is not shown as live: %+v", view)
	}
}

// What the operator is told about servers running out and about the way
// from an entry server into the country going bad.
func TestOperatorIsToldAboutRenewalsAndLineQuality(t *testing.T) {
	c := newTestAPI(t)
	ctx := context.Background()
	st := c.api.Store
	// 21:00 on October 8th in Shanghai.
	now := time.Date(2026, 10, 8, 13, 0, 0, 0, time.UTC)
	st.Now = func() time.Time { return now }
	tgChat := &chat{}
	fake := httptest.NewServer(tgChat)
	defer fake.Close()
	tg := notify.New(func(context.Context) (string, string) { return "1234567890:AAEabcdefghijklmnopqrstuvwxyz012345", "42" }, nil)
	tg.Base = fake.URL
	rep, err := report.New(ctx, st, c.api.Traffic, tg, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0

	c.do("POST", "/api/v1/auth/setup", map[string]any{"setup_token": testSetupToken, "username": "admin", "password": "password123"}, 200)
	c.do("PUT", "/api/v1/settings", map[string]any{"quota.timezone": "Asia/Shanghai"}, 200)
	entry := c.do("POST", "/api/v1/servers", map[string]any{"name": "搬瓦工", "price": 49.99, "currency": "$", "cycle": "year", "expires_at": "2026-10-14"}, 201)
	landing := c.do("POST", "/api/v1/servers", map[string]any{"name": "AT&T", "price": 8, "currency": "$", "cycle": "month", "expires_at": "2026-10-09", "auto_renew": true}, 201)
	watched := c.do("POST", "/api/v1/servers", map[string]any{"name": "tyo-1", "expires_at": "2027-01-01"}, 201)
	in := c.do("POST", "/api/v1/servers/"+itoa(entry["id"])+"/nodes", map[string]any{"name": "入口", "protocol": "vless", "port": 443, "sni": "mirrors.example.edu"}, 201)
	out := c.do("POST", "/api/v1/servers/"+itoa(landing["id"])+"/nodes", map[string]any{"name": "落地", "protocol": "vless", "port": 26903, "sni": "www.example.com"}, 201)
	c.do("POST", "/api/v1/lines", map[string]any{"name": "搬瓦工直连", "entry_node_id": in["id"], "sort_order": 1}, 201)
	c.do("POST", "/api/v1/lines", map[string]any{"name": "搬瓦工 → AT&T", "entry_node_id": in["id"], "landing_node_id": out["id"], "sort_order": 2}, 201)
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	renew := func() {
		t.Helper()
		if err := rep.Renewals(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// ---- renewals ----
	renew()
	got := tgChat.next(t, &seen, 2)
	has(t, got[0], "<b>⏳ 搬瓦工 还有 6 天到期</b>", "到期日 2026-10-14。续费 $49.99/年。", "面板 → 服务器 → 搬瓦工 → 编辑")
	has(t, got[1], "<b>⏳ AT&amp;T 明天自动续费</b>", "会扣 $8.00/月")
	renew()
	tgChat.next(t, &seen, 0)
	// The overview says the same for as long as it holds.
	var notes []string
	for _, a := range c.do("GET", "/api/v1/dashboard", nil, 200)["alerts"].([]any) {
		notes = append(notes, a.(map[string]any)["level"].(string)+" "+a.(map[string]any)["message"].(string))
	}
	has(t, strings.Join(notes, "\n"), "warn 搬瓦工 还有 6 天 到期（2026-10-14）", "info AT&T 明天 自动续费")

	// The day passes: the one that renews by itself moves on a month without
	// a word; the other is said to have lapsed.
	now = time.Date(2026, 10, 15, 13, 0, 0, 0, time.UTC)
	renew()
	has(t, tgChat.next(t, &seen, 1)[0], "<b>⛔ 搬瓦工 的到期日过了</b>", "到期日是 2026-10-14，已经过了 1 天")
	if alerts := c.do("GET", "/api/v1/dashboard", nil, 200)["alerts"].([]any); len(alerts) != 1 || alerts[0].(map[string]any)["kind"] != "lapsed" {
		t.Fatalf("a day that has passed is the only thing left to say: %v", alerts)
	}
	if s, _ := st.GetServer(ctx, id(landing)); s.ExpiresAt != "2026-11-09" {
		t.Fatalf("an automatic renewal moves the day on by one cycle: %q", s.ExpiresAt)
	}
	c.do("PUT", "/api/v1/servers/"+itoa(entry["id"]), map[string]any{"name": "搬瓦工", "price": 49.99, "currency": "$", "cycle": "year", "expires_at": "2027-10-14"}, 200)
	renew()
	has(t, tgChat.next(t, &seen, 1)[0], "<b>🟢 搬瓦工 的到期日更新了</b>", "新的到期日 2027-10-14")
	if tgChat.reply[seen-1] != 103 {
		t.Fatalf("the all-clear answers the alert: reply to %d", tgChat.reply[seen-1])
	}

	// ---- line quality ----
	targets, err := st.ReplaceProbeTargets(ctx, []domain.ProbeTarget{{Name: "四川电信", Host: "sc-ct-v4.ip.zstaticcdn.com", Port: 80, Carrier: "ct", OnCard: true}, {Name: "四川联通", Host: "sc-cu-v4.ip.zstaticcdn.com", Port: 80, Carrier: "cu"}})
	if err != nil {
		t.Fatal(err)
	}
	ct, cu := targets[0].ID, targets[1].ID
	// Every server has reported, and each pair has a usual round trip of
	// 180 ms from the day before.
	for _, s := range []map[string]any{entry, landing, watched} {
		et := c.do("POST", "/api/v1/servers/"+itoa(s["id"])+"/enroll-token", nil, 200)
		admin := c.cookie
		c.cookie = nil
		c.agent = c.do("POST", "/api/agent/v1/enroll", agentproto.EnrollRequest{EnrollToken: et["token"].(string), Version: "test", Arch: "amd64"}, 200)["agent_token"].(string)
		c.do("POST", "/api/agent/v1/heartbeat", agentproto.Heartbeat{Epoch: "boot", TS: now, Metrics: agentproto.Metrics{MemTotal: 1000}}, 200)
		c.cookie, c.agent = admin, ""
	}
	var usual []store.ProbePoint
	for h := 1; h <= 8; h++ {
		ts := now.Add(-time.Duration(h)*time.Hour).Unix() / 3600 * 3600
		for _, s := range []map[string]any{entry, landing, watched} {
			for _, target := range []int64{ct, cu} {
				usual = append(usual, store.ProbePoint{ServerID: id(s), TargetID: target, TS: ts, Sent: 60, RTTSum: 60 * 180_000, RTTMin: 170_000, RTTMax: 200_000})
			}
		}
	}
	if err := st.AddLiveMinutes(ctx, nil, usual); err != nil {
		t.Fatal(err)
	}
	window := func(loss map[[2]int64]int, avgMs map[[2]int64]int64) map[store.ProbeKey]store.ProbePoint {
		w := map[store.ProbeKey]store.ProbePoint{}
		for _, s := range []map[string]any{entry, landing, watched} {
			for _, target := range []int64{ct, cu} {
				k := [2]int64{id(s), target}
				avg := int64(180)
				if v, ok := avgMs[k]; ok {
					avg = v
				}
				p := store.ProbePoint{ServerID: k[0], TargetID: target, Sent: 60, Lost: loss[k]}
				p.RTTSum = int64(p.Sent-p.Lost) * avg * 1000
				w[store.ProbeKey{ServerID: k[0], TargetID: target}] = p
			}
		}
		return w
	}
	quality := func(w map[store.ProbeKey]store.ProbePoint) {
		t.Helper()
		if err := rep.Quality(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	quality(window(nil, nil))
	tgChat.next(t, &seen, 0)

	// The daily report says how far each entry was from the targets shown
	// on the cards; the other servers and targets are left out.
	if err := rep.PostDaily(ctx); err != nil {
		t.Fatal(err)
	}
	daily := tgChat.next(t, &seen, 1)[0]
	has(t, daily, "<b>入口到国内（今天）</b>\n<b>搬瓦工</b>　四川电信 180 ms，丢包 0%\n\n")
	if strings.Contains(daily, "四川联通") || strings.Contains(daily, "AT&amp;T</b>　四川") {
		t.Fatalf("only entries and the targets on the cards belong in the report:\n%s", daily)
	}

	// The landing and the watched server lose packets into the country:
	// nobody connects to them from there, so nothing is said. The entry
	// losing a quarter of its probes is.
	quality(window(map[[2]int64]int{{id(landing), ct}: 30, {id(watched), cu}: 45, {id(entry), ct}: 15}, nil))
	bad := tgChat.next(t, &seen, 1)[0]
	has(t, bad, "<b>📶 搬瓦工 到四川电信 丢包 25%</b>", "近 10 分钟：丢包 25%，延迟 180 ms（平时 180 ms）", "从四川电信连这些线路会卡：搬瓦工直连、搬瓦工 → AT&amp;T")
	// Still bad, and hovering between bad and good: said once.
	quality(window(map[[2]int64]int{{id(entry), ct}: 20}, nil))
	quality(window(map[[2]int64]int{{id(entry), ct}: 6}, nil))
	tgChat.next(t, &seen, 0)
	now = now.Add(25 * time.Minute)
	quality(window(nil, nil))
	has(t, tgChat.next(t, &seen, 1)[0], "<b>🟢 搬瓦工 到四川电信恢复正常</b>", "差了 25 分钟。现在丢包 0%，延迟 180 ms")
	if tgChat.reply[seen-1] != 106 {
		t.Fatalf("the all-clear answers the alert: reply to %d", tgChat.reply[seen-1])
	}

	// Twice the usual round trip, with nothing lost, is told as slow.
	quality(window(nil, map[[2]int64]int64{{id(entry), cu}: 390}))
	has(t, tgChat.next(t, &seen, 1)[0], "<b>📶 搬瓦工 到四川联通 延迟升到 390 ms</b>", "丢包 0%，延迟 390 ms（平时 180 ms）")
	quality(window(nil, nil))
	tgChat.next(t, &seen, 1)

	// A target every server loses at once is the target's own trouble, and
	// while it lasts the entry's way to it is not judged.
	all := map[[2]int64]int{{id(entry), ct}: 60, {id(landing), ct}: 60, {id(watched), ct}: 58}
	quality(window(all, nil))
	has(t, tgChat.next(t, &seen, 1)[0], "<b>⚠️ 探测目标 四川电信 连不上了</b>", "3 台服务器里有 3 台同时连不上 sc-ct-v4.ip.zstaticcdn.com:80")
	quality(window(all, nil))
	tgChat.next(t, &seen, 0)
	quality(window(nil, nil))
	has(t, tgChat.next(t, &seen, 1)[0], "<b>🟢 探测目标 四川电信 恢复了</b>")

	// Turned off in the settings, what is open ends without a word.
	quality(window(map[[2]int64]int{{id(entry), cu}: 30}, nil))
	tgChat.next(t, &seen, 1)
	c.do("PUT", "/api/v1/settings", map[string]any{"telegram.quality_alerts": "0"}, 200)
	quality(window(nil, nil))
	quality(window(map[[2]int64]int{{id(entry), cu}: 30}, nil))
	tgChat.next(t, &seen, 0)
}
