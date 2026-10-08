package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ctlvps/internal/agentproto"
	"ctlvps/internal/domain"
	"ctlvps/internal/notify"
	"ctlvps/internal/report"
	"ctlvps/internal/store"
)

// chat is a Telegram that keeps what it was sent.
type chat struct {
	mu    sync.Mutex
	texts []string
	reply []int64
}

func (c *chat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text  string `json:"text"`
		Reply struct {
			MessageID int64 `json:"message_id"`
		} `json:"reply_parameters"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.texts = append(c.texts, in.Text)
	c.reply = append(c.reply, in.Reply.MessageID)
	fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d}}`, 100+len(c.texts))
}

// next returns the messages that arrived since it was last called.
func (c *chat) next(t *testing.T, seen *int, want int) []string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	got := c.texts[*seen:]
	if len(got) != want {
		t.Fatalf("want %d new messages, got %d:\n%s", want, len(got), strings.Join(got, "\n---\n"))
	}
	*seen = len(c.texts)
	return got
}

func has(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(text, p) {
			t.Fatalf("message lacks %q:\n%s", p, text)
		}
	}
}

func TestOperatorIsToldOnceWithWhatMatters(t *testing.T) {
	c := newTestAPI(t)
	ctx := context.Background()
	st := c.api.Store
	// 21:00 in Shanghai.
	now := time.Date(2026, 10, 7, 13, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	st.Now, c.api.Traffic.Now, c.api.Shares.Now = clock, clock, clock

	tgChat := &chat{}
	fake := httptest.NewServer(tgChat)
	defer fake.Close()
	tg := notify.New(func(context.Context) (string, string) { return "1234567890:AAEabcdefghijklmnopqrstuvwxyz012345", "42" }, nil)
	tg.Base = fake.URL
	newReporter := func() *report.Reporter {
		rep, err := report.New(ctx, st, c.api.Traffic, tg, nil)
		if err != nil {
			t.Fatal(err)
		}
		c.api.Report = rep
		c.api.Shares.OnEvent = func(ctx context.Context, sh domain.Share, _, _ string) {
			if cur, err := st.GetShare(ctx, sh.ID); err == nil {
				rep.User(ctx, cur)
			}
		}
		return rep
	}
	rep := newReporter()
	seen := 0

	c.do("POST", "/api/v1/auth/setup", map[string]any{"setup_token": testSetupToken, "username": "admin", "password": "password123"}, 200)
	c.do("PUT", "/api/v1/settings", map[string]any{"quota.timezone": "Asia/Shanghai"}, 200)
	admin := c.cookie
	asAdmin := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		agent := c.agent
		c.cookie, c.agent = admin, ""
		defer func() { c.cookie, c.agent = nil, agent }()
		return c.do(method, path, body, want)
	}
	entry := asAdmin("POST", "/api/v1/servers", map[string]any{"name": "入口机", "public_host": "198.51.100.1", "quota_bytes": 1000, "quota_reset_day": 1}, 201)
	landing := asAdmin("POST", "/api/v1/servers", map[string]any{"name": "落地机", "public_host": "203.0.113.9"}, 201)
	probe := asAdmin("POST", "/api/v1/servers", map[string]any{"name": "探针", "public_host": "192.0.2.7"}, 201)
	in := asAdmin("POST", "/api/v1/servers/"+itoa(entry["id"])+"/nodes", map[string]any{"name": "入口", "protocol": "vless", "port": 443, "sni": "mirrors.example.edu"}, 201)
	out := asAdmin("POST", "/api/v1/servers/"+itoa(landing["id"])+"/nodes", map[string]any{"name": "落地", "protocol": "vless", "port": 26903, "sni": "www.example.com"}, 201)
	asAdmin("POST", "/api/v1/lines", map[string]any{"name": "直连 <LA>", "entry_node_id": in["id"], "sort_order": 1}, 201)
	asAdmin("POST", "/api/v1/lines", map[string]any{"name": "家宽", "entry_node_id": in["id"], "landing_node_id": out["id"], "sort_order": 2}, 201)
	yang := asAdmin("POST", "/api/v1/shares", map[string]any{"name": "YANG", "line_mode": "all", "quota_bytes": 1000, "reset_day": 1}, 201)
	asAdmin("POST", "/api/v1/shares", map[string]any{"name": "sansan", "line_mode": "all"}, 201)

	tokens := map[string]string{}
	for name, srv := range map[string]map[string]any{"entry": entry, "landing": landing, "probe": probe} {
		et := asAdmin("POST", "/api/v1/servers/"+itoa(srv["id"])+"/enroll-token", nil, 200)
		c.cookie = nil
		tokens[name] = c.do("POST", "/api/agent/v1/enroll", agentproto.EnrollRequest{EnrollToken: et["token"].(string), Version: "test", Arch: "amd64"}, 200)["agent_token"].(string)
	}
	beat := func(name string, rx, tx int64) {
		t.Helper()
		c.cookie, c.agent = nil, tokens[name]
		c.do("POST", "/api/agent/v1/heartbeat", agentproto.Heartbeat{Epoch: "boot", TS: now, Metrics: agentproto.Metrics{NetRx: rx, NetTx: tx, MemTotal: 1000, MemUsed: 610, CPUPercent: 3, UptimeSec: 86400}}, 200)
	}
	watch := func() {
		t.Helper()
		if err := rep.WatchAgents(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// ---- a server goes quiet and comes back ----
	for _, name := range []string{"entry", "landing", "probe"} {
		beat(name, 1, 1)
	}
	now = now.Add(3 * time.Minute)
	beat("entry", 1, 1)
	beat("landing", 1, 1)
	beat("probe", 1, 1)
	watch()
	tgChat.next(t, &seen, 0)

	now = now.Add(5 * time.Minute)
	beat("entry", 1, 1)
	beat("probe", 1, 1)
	watch()
	lost := tgChat.next(t, &seen, 1)[0]
	has(t, lost, "<b>🔴 落地机 失联</b>", "最后心跳 21:03", "5 分钟", "失联前：CPU 3% · 内存 61%", "· 家宽", "现在能用的线路：\n· 直连 &lt;LA&gt;")
	watch()
	tgChat.next(t, &seen, 0)

	// The panel restarts: what was said is not said again, and the all-clear
	// still answers it.
	rep = newReporter()
	now = now.Add(3 * time.Minute)
	beat("entry", 1, 1)
	beat("probe", 1, 1)
	watch()
	tgChat.next(t, &seen, 0)
	now = now.Add(4 * time.Minute)
	beat("entry", 1, 1)
	beat("probe", 1, 1)
	beat("landing", 1, 1)
	watch()
	has(t, tgChat.next(t, &seen, 1)[0], "<b>🟢 落地机 恢复</b>", "失联了 12 分钟（21:03 – 21:15）", "经过它的线路：家宽")
	if tgChat.reply[seen-1] != 101 {
		t.Fatalf("the all-clear must answer the alert: reply to %d", tgChat.reply[seen-1])
	}

	// Servers that go quiet together are one message; a server nothing runs
	// through is said to be harmless.
	now = now.Add(5 * time.Minute)
	beat("entry", 1, 1)
	watch()
	has(t, tgChat.next(t, &seen, 1)[0], "<b>🔴 2 台服务器同时失联</b>", "· 落地机（最后心跳 21:15）", "· 探针（最后心跳 21:15）", "多半是主控自己的网络", "· 家宽")
	now = now.Add(time.Minute)
	beat("landing", 1, 1)
	beat("probe", 1, 1)
	beat("entry", 1, 1)
	watch()
	has(t, tgChat.next(t, &seen, 1)[0], "<b>🟢 2 台服务器恢复</b>", "· 落地机：失联了 6 分钟")

	// ---- a server nears and passes its quota ----
	beat("entry", 401, 451)
	warn := tgChat.next(t, &seen, 1)[0]
	has(t, warn, "<b>⚠️ 入口机 本期流量用到 85%</b>", "已用 850 B / 1000 B（双向都算），还剩 150 B", "11-01 重置，还有 25 天", "没有开“配额用完就停”")
	beat("entry", 402, 452)
	tgChat.next(t, &seen, 0)
	beat("entry", 501, 551)
	has(t, tgChat.next(t, &seen, 1)[0], "<b>⛔ 入口机 本期流量用完了</b>", "经过它的线路：直连 &lt;LA&gt;、家宽")

	// ---- a user nears their quota, runs out, and is given more ----
	id := int64(yang["id"].(float64))
	if _, _, err := st.AddShareUsage(ctx, id, 100, 750); err != nil {
		t.Fatal(err)
	}
	if err := rep.Users(ctx); err != nil {
		t.Fatal(err)
	}
	has(t, tgChat.next(t, &seen, 1)[0], "<b>⚠️ YANG 本期流量用到 85%</b>", "已用 850 B / 1000 B（上传 100 B，下载 750 B），还剩 150 B", "11-01 重置", "面板 → 用户 → YANG")
	if _, _, err := st.AddShareUsage(ctx, id, 0, 200); err != nil {
		t.Fatal(err)
	}
	if err := c.api.Shares.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	has(t, tgChat.next(t, &seen, 1)[0], "<b>⛔ YANG 本期流量用完了，线路已停</b>", "11-01 重置后自动恢复")
	stopped := seen
	now = now.Add(30 * time.Minute)
	asAdmin("PUT", "/api/v1/shares/"+itoa(yang["id"]), map[string]any{"name": "YANG", "line_mode": "all", "quota_bytes": 100000, "reset_day": 1}, 200)
	has(t, tgChat.next(t, &seen, 1)[0], "<b>🟢 YANG 的线路恢复了</b>", "停了 30 分钟")
	if tgChat.reply[seen-1] != int64(100+stopped) {
		t.Fatalf("the all-clear must answer the alert: reply to %d", tgChat.reply[seen-1])
	}

	// ---- a server's own health ----
	srv, _ := st.GetServer(ctx, int64(landing["id"].(float64)))
	sick := agentproto.Diagnostics{Cores: []agentproto.CoreStatus{{Name: "sing-box", Wanted: true, Active: false, LastError: "exit <1>"}}, ClockSkewMs: -12000}
	rep.Diagnostics(ctx, srv, agentproto.Metrics{}, sick)
	got := tgChat.next(t, &seen, 2)
	has(t, got[0], "<b>🔴 落地机 上的 sing-box 没在运行</b>", "<code>exit &lt;1&gt;</code>", "· 家宽")
	has(t, got[1], "<b>🟠 落地机 的时钟差了 12 秒</b>", "30 秒")
	rep.Diagnostics(ctx, srv, agentproto.Metrics{}, sick)
	tgChat.next(t, &seen, 0)
	now = now.Add(2 * time.Minute)
	// Within the band the clock is neither told again nor cleared.
	rep.Diagnostics(ctx, srv, agentproto.Metrics{}, agentproto.Diagnostics{Cores: []agentproto.CoreStatus{{Name: "sing-box", Wanted: true, Active: true}}, ClockSkewMs: 3000})
	has(t, tgChat.next(t, &seen, 1)[0], "<b>🟢 落地机 上的 sing-box 恢复运行</b>", "停了 2 分钟")
	rep.Diagnostics(ctx, srv, agentproto.Metrics{}, agentproto.Diagnostics{ClockSkewMs: 100})
	has(t, tgChat.next(t, &seen, 1)[0], "<b>🟢 落地机 的时钟对上了</b>")

	rep.Applied(ctx, srv, 7, "bind: address already in use")
	rep.Applied(ctx, srv, 8, "bind: address already in use")
	has(t, tgChat.next(t, &seen, 1)[0], "<b>🔴 落地机 配置下发失败</b>", "第 7 版", "<code>bind: address already in use</code>")
	rep.Applied(ctx, srv, 9, "")
	has(t, tgChat.next(t, &seen, 1)[0], "<b>🟢 落地机 配置下发成功</b>", "第 9 版")

	// ---- the daily report ----
	// What YANG carried today went over the relay line, on both machines.
	members, err := st.ListNodes(ctx, store.NodeFilter{ShareID: &id})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range members {
		if n.LineID != nil && n.Landing {
			for _, m := range members {
				if m.LineID != nil && *m.LineID == *n.LineID {
					_ = st.AddTraffic(ctx, store.SubjectNode, m.ID, now, 2<<20, 6<<20)
				}
			}
		}
	}
	_ = st.AddTraffic(ctx, store.SubjectShare, id, now, 4<<20, 12<<20)
	now = now.Add(time.Minute)
	for _, name := range []string{"entry", "landing", "probe"} {
		beat(name, 501, 551)
	}
	tgChat.next(t, &seen, 0)
	if err := rep.PostDaily(ctx); err != nil {
		t.Fatal(err)
	}
	daily := tgChat.next(t, &seen, 1)[0]
	// Who carried the day and over which line; a user who carried next to
	// nothing only by name. Servers that lines run through or that have a
	// quota; one that is only watched and well is left out.
	has(t, daily, "📊 <b>土豆饼的家 · 10-07 周三 日报</b>\n⚠️ 3 台服务器都在线 · 2 条线路都可用 · 1 件事还没解决",
		"<b>👤 用户 · 今天</b>\n<b>YANG</b>　16.0 MB\n　家宽\n不到 10 MB：sansan\n\n",
		"<b>🖥 服务器 · 今天</b>\n<b>入口机</b>　1.03 KB · 本期 105%\n<b>落地机</b>　",
		"<b>🗓 今天发生的事</b>", "· 21:03 🔴 落地机 失联，持续 12 分钟", "· 21:15 🔴 探针 失联，持续 6 分钟", "· 21:21 ⚠️ YANG 本期流量用到 85%\n", "⛔ YANG 本期流量用完了，线路已停，持续 30 分钟",
		"· 21:53 🔴 落地机 配置下发失败，持续不到 1 分钟",
		"<b>⏳ 还没解决</b>", "· ⛔ 入口机 本期流量用完了（21:21 起，已经 33 分钟）")
	if strings.Contains(daily, "<b>探针</b>") {
		t.Fatalf("a watched server that is well has no line:\n%s", daily)
	}
	if strings.Contains(daily, "要留意") {
		t.Fatalf("nothing runs short here:\n%s", daily)
	}

	// Once a day, in the chosen hour on the panel's clock.
	asAdmin("PUT", "/api/v1/settings", map[string]any{"telegram.daily_report": "1", "telegram.daily_hour": "22"}, 200)
	if err := rep.DailyDue(ctx); err != nil {
		t.Fatal(err)
	}
	tgChat.next(t, &seen, 0)
	now = now.Add(10 * time.Minute) // 22:03 in Shanghai
	for i := 0; i < 2; i++ {
		if err := rep.DailyDue(ctx); err != nil {
			t.Fatal(err)
		}
	}
	tgChat.next(t, &seen, 1)
	// In the morning the report is about the day before.
	now = time.Date(2026, 10, 8, 0, 30, 0, 0, time.UTC) // 08:30 in Shanghai
	if err := rep.PostDaily(ctx); err != nil {
		t.Fatal(err)
	}
	has(t, tgChat.next(t, &seen, 1)[0], "10-07 周三 日报", "<b>👤 用户 · 昨天</b>\n<b>YANG</b>　16.0 MB", "<b>🗓 昨天发生的事</b>\n· 21:03 🔴 落地机 失联", "<b>📌 要留意</b>", "· YANG：近 7 天日均 16.0 MB，照这个速度约 10-08 用完，比重置早 24 天。")

	// Told to stop when its quota is used up, the server says so in place of
	// the plain "used up", and answers that once it carries traffic again.
	path := "/api/v1/servers/" + itoa(entry["id"])
	asAdmin("PUT", path, map[string]any{"name": "入口机", "public_host": "198.51.100.1", "quota_bytes": 1000, "quota_reset_day": 1, "quota_stop": true}, 200)
	has(t, tgChat.next(t, &seen, 1)[0], "<b>⛔ 入口机 配额用完，入站已停</b>", "11-01 重置后自动恢复", "停掉的线路：\n· 直连 &lt;LA&gt;\n· 家宽", "其余线路现在也都用不了")
	halted := seen
	now = now.Add(time.Hour)
	asAdmin("PUT", path+"/usage", map[string]any{"used_bytes": 10}, 200)
	has(t, tgChat.next(t, &seen, 1)[0], "<b>🟢 入口机 的入站恢复了</b>", "停了 1 小时。现在已用 10 B / 1000 B")
	if tgChat.reply[seen-1] != int64(100+halted) {
		t.Fatalf("the all-clear must answer the alert: reply to %d", tgChat.reply[seen-1])
	}

	// A server that is removed takes what was open about it along.
	asAdmin("PUT", path+"/usage", map[string]any{"used_bytes": 900}, 200)
	tgChat.next(t, &seen, 1)
	asAdmin("DELETE", "/api/v1/servers/"+itoa(entry["id"]), nil, 204)
	beat("landing", 501, 551)
	beat("probe", 501, 551)
	watch()
	if err := rep.PostDaily(ctx); err != nil {
		t.Fatal(err)
	}
	if last := tgChat.next(t, &seen, 1)[0]; strings.Contains(last, "还没解决") {
		t.Fatalf("an incident outlived its server:\n%s", last)
	}
}
