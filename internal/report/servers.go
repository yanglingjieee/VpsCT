package report

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"ctlvps/internal/agentproto"
	"ctlvps/internal/domain"
	"ctlvps/internal/store"
	"ctlvps/internal/traffic"
)

// returned is a server that reports again, with the record of its silence.
type returned struct {
	server domain.Server
	inc    domain.Incident
}

// WatchAgents tells when servers stop reporting and when they are back.
// Servers that go quiet in the same minute are told in one message: together
// they usually have one cause.
func (r *Reporter) WatchAgents(ctx context.Context) error {
	if r == nil {
		return nil
	}
	sc, err := r.look(ctx)
	if err != nil {
		return err
	}
	r.sweep(ctx, sc)
	// Nobody can have reported while the panel itself was not running.
	if r.Store.Now().Sub(r.started) < sc.silence {
		return nil
	}
	var lost []domain.Server
	var back []returned
	watched := 0
	for _, s := range sc.servers {
		k := key("offline", "server", s.ID)
		switch {
		case !s.Enabled || !sc.enrolled(s.ID):
			r.end(ctx, k)
		case sc.lost(s.ID):
			watched++
			if !r.isOpen(k) {
				lost = append(lost, s)
			}
		default:
			watched++
			if inc, ok := r.end(ctx, k); ok {
				back = append(back, returned{s, inc})
			}
		}
	}
	if len(lost) > 0 {
		var began []domain.Incident
		for _, s := range lost {
			if inc, ok := r.begin(ctx, key("offline", "server", s.ID), "🔴 "+s.Name+" 失联", *sc.agent[s.ID].LastSeenAt); ok {
				began = append(began, inc)
			}
		}
		if len(began) > 0 {
			message := r.post(ctx, sc.lostNotice(lost, watched), 0)
			for _, inc := range began {
				r.attach(ctx, inc, message)
			}
		}
	}
	if len(back) > 0 {
		answer := back[0].inc.MessageID
		for _, b := range back {
			if b.inc.MessageID != answer {
				answer = 0
			}
		}
		r.post(ctx, sc.backNotice(back), answer)
	}
	return nil
}

func (sc *scene) lostNotice(lost []domain.Server, watched int) notice {
	ids := make([]int64, 0, len(lost))
	for _, s := range lost {
		ids = append(ids, s.ID)
	}
	if len(lost) == 1 {
		s := lost[0]
		seen := *sc.agent[s.ID].LastSeenAt
		n := notice{title: "🔴 " + s.Name + " 失联"}
		n.body = append(n.body, fmt.Sprintf("最后心跳 %s，主控已经%s没有收到它的消息。", at(sc.now, seen), span(sc.now.Sub(seen))))
		if v := vitals(sc.metrics(s.ID)); v != "" {
			n.body = append(n.body, "失联前："+v)
		}
		n.body = append(n.body, sc.routes("经过它的线路可能不通（主控联系不上它，线路本身不一定断）：", s.ID)...)
		return n
	}
	n := notice{title: fmt.Sprintf("🔴 %d 台服务器同时失联", len(lost))}
	for _, s := range lost {
		n.body = append(n.body, fmt.Sprintf("· %s（最后心跳 %s）", esc(s.Name), at(sc.now, *sc.agent[s.ID].LastSeenAt)))
	}
	if len(lost)*2 >= watched {
		n.body = append(n.body, "这么多台同时失联，多半是主控自己的网络出了问题，线路未必受影响。")
	}
	if hit := sc.through(ids...); len(hit) > 0 {
		n.body = append(n.body, sc.routes("经过它们的线路可能不通：", ids...)...)
	} else {
		n.body = append(n.body, "没有线路经过它们，不影响上网。")
	}
	return n
}

func (sc *scene) backNotice(back []returned) notice {
	// A machine that has been up for less time than it was silent restarted.
	rebooted := func(b returned) (time.Duration, bool) {
		up := time.Duration(sc.metrics(b.server.ID).UptimeSec) * time.Second
		return up, up > 0 && up < sc.now.Sub(b.inc.OpenedAt)
	}
	if len(back) == 1 {
		b := back[0]
		n := notice{title: "🟢 " + b.server.Name + " 恢复"}
		n.body = append(n.body, fmt.Sprintf("失联了%s（%s – %s）。", span(sc.now.Sub(b.inc.OpenedAt)), at(sc.now, b.inc.OpenedAt), at(sc.now, sc.now)))
		if up, yes := rebooted(b); yes {
			n.body = append(n.body, fmt.Sprintf("机器重启过，开机%s。", span(up)))
		}
		if v := vitals(sc.metrics(b.server.ID)); v != "" {
			n.body = append(n.body, "现在："+v)
		}
		if hit := sc.through(b.server.ID); len(hit) > 0 {
			n.body = append(n.body, "经过它的线路："+esc(strings.Join(hit, "、")))
		}
		return n
	}
	n := notice{title: fmt.Sprintf("🟢 %d 台服务器恢复", len(back))}
	for _, b := range back {
		line := fmt.Sprintf("· %s：失联了%s", esc(b.server.Name), span(sc.now.Sub(b.inc.OpenedAt)))
		if _, yes := rebooted(b); yes {
			line += "，重启过"
		}
		n.body = append(n.body, line)
	}
	return n
}

// pace is what a subject carried per day over the last week, counted by per;
// 0 when it carried nothing. A subject younger than a week is averaged over
// the days it has.
func (r *Reporter) pace(ctx context.Context, subject string, id int64, per func(up, down int64) int64) int64 {
	series, err := r.Traffic.Daily(ctx, subject, id, 8)
	if err != nil || len(series.Points) < 2 {
		return 0
	}
	days := series.Points[:len(series.Points)-1] // today is not over yet
	var sum int64
	first := -1
	for i, p := range days {
		v := per(p.Up, p.Down)
		if v > 0 && first < 0 {
			first = i
		}
		sum += v
	}
	if first < 0 {
		return 0
	}
	return sum / int64(len(days)-first)
}

// outlook says whether what is left lasts until the reset at the recent
// pace; short is true when it does not.
func outlook(now time.Time, left, perDay int64, reset *time.Time) (text string, short bool) {
	if perDay <= 0 || left <= 0 {
		return "", false
	}
	out := now.Add(time.Duration(float64(left) / float64(perDay) * float64(24*time.Hour)))
	text = "近 7 天日均 " + size(perDay) + "，照这个速度"
	switch {
	case reset == nil:
		return text + "约 " + date(now, out) + " 用完。", false
	case daysUntil(out, *reset) >= 1:
		return text + fmt.Sprintf("约 %s 用完，比重置早 %d 天。", date(now, out), daysUntil(out, *reset)), true
	}
	return text + "够用到重置。", false
}

// carriers is what each user carried on a server in the last week, most
// first; empty when nobody did.
func (r *Reporter) carriers(ctx context.Context, serverID int64) string {
	nodes, err := r.Store.ListNodes(ctx, store.NodeFilter{ServerID: &serverID, IncludeRevoked: true})
	if err != nil {
		return ""
	}
	week, err := r.Store.NodeTrafficSummaries(ctx, 7)
	if err != nil {
		return ""
	}
	shares, err := r.Store.ListShares(ctx, nil)
	if err != nil {
		return ""
	}
	used := map[int64]int64{}
	for _, n := range nodes {
		if n.ShareID != nil {
			used[*n.ShareID] += week[n.ID].Total
		}
	}
	sort.SliceStable(shares, func(i, j int) bool { return used[shares[i].ID] > used[shares[j].ID] })
	var parts []string
	for _, sh := range shares {
		if used[sh.ID] > 0 && len(parts) < 5 {
			parts = append(parts, esc(sh.Name)+" "+size(used[sh.ID]))
		}
	}
	return strings.Join(parts, " · ")
}

// serverBilled counts a server's traffic the way its host does.
func serverBilled(s domain.Server) func(rx, tx int64) int64 {
	return func(rx, tx int64) int64 { return traffic.Billed(s.QuotaBilling, rx, tx) }
}

// ServerQuota tells when a server nears its quota, uses it up, is stopped
// for it and carries traffic again. s is the server as it now stands.
func (r *Reporter) ServerQuota(ctx context.Context, s domain.Server, u traffic.ServerUsage) {
	if r == nil {
		return
	}
	limited := s.QuotaBytes > 0
	alert := float64(r.Store.GetSettingInt(ctx, domain.SettingQuotaAlertPct, 80))
	now := r.Store.Now().In(r.Store.Location())
	used := fmt.Sprintf("已用 %s / %s（%s）", size(u.Billed), size(u.Quota), billingName(s.QuotaBilling))
	period := func() string {
		if u.NextReset == nil {
			return "按最近 30 天滚动计算，没有重置日。"
		}
		return fmt.Sprintf("本期 %s 起，%s 重置，%s。", date(now, u.PeriodStart), date(now, *u.NextReset), inDays(now, *u.NextReset))
	}

	r.track(ctx, key("quota", "server", s.ID), limited && u.Percent >= alert && !u.OverQuota, func() notice {
		n := notice{title: fmt.Sprintf("⚠️ %s 本期流量用到 %s", s.Name, percent(u.Percent))}
		n.body = append(n.body, used+"，还剩 "+size(u.Quota-u.Billed)+"。", period())
		if text, _ := outlook(now, u.Quota-u.Billed, r.pace(ctx, store.SubjectServer, s.ID, serverBilled(s)), u.NextReset); text != "" {
			n.body = append(n.body, text)
		}
		if who := r.carriers(ctx, s.ID); who != "" {
			n.body = append(n.body, "近 7 天在这台上："+who)
		}
		if s.QuotaStop {
			n.body = append(n.body, "用完后这台的入站会自动停，到重置再恢复。")
		} else {
			n.body = append(n.body, "这台没有开“配额用完就停”，用完后只提醒，线路照常。")
		}
		return n
	}, nil)

	r.track(ctx, key("over", "server", s.ID), limited && u.OverQuota && !s.QuotaStopped, func() notice {
		n := notice{title: "⛔ " + s.Name + " 本期流量用完了"}
		n.body = append(n.body, used+"。", period(), "这台没有开“配额用完就停”，线路照常跑；超出的部分服务商可能限速或另外收费。")
		if who := r.carriers(ctx, s.ID); who != "" {
			n.body = append(n.body, "近 7 天在这台上："+who)
		}
		if sc, err := r.look(ctx); err == nil {
			if hit := sc.through(s.ID); len(hit) > 0 {
				n.body = append(n.body, "经过它的线路："+esc(strings.Join(hit, "、")))
			}
		}
		return n
	}, nil)

	r.track(ctx, key("stop", "server", s.ID), s.QuotaStopped, func() notice {
		n := notice{title: "⛔ " + s.Name + " 配额用完，入站已停"}
		n.body = append(n.body, used+"。")
		if u.NextReset != nil {
			n.body = append(n.body, fmt.Sprintf("%s 重置后自动恢复，%s。想提前恢复：调大配额，或校正本期已用。", date(now, *u.NextReset), inDays(now, *u.NextReset)))
		} else {
			n.body = append(n.body, "用量回到配额以内就恢复：调大配额，或校正本期已用。")
		}
		n.body = append(n.body, "机器本身、SSH 和监控不受影响。")
		if sc, err := r.look(ctx); err == nil {
			n.body = append(n.body, sc.routes("停掉的线路：", s.ID)...)
		}
		return n
	}, func(inc domain.Incident) notice {
		n := notice{title: "🟢 " + s.Name + " 的入站恢复了"}
		n.body = append(n.body, fmt.Sprintf("停了%s。现在%s。", span(now.Sub(inc.OpenedAt)), used))
		if sc, err := r.look(ctx); err == nil {
			if hit := sc.through(s.ID); len(hit) > 0 {
				n.body = append(n.body, "经过它的线路："+esc(strings.Join(hit, "、")))
			}
		}
		return n
	})
}

// Diagnostics tells about a server's own health as its agent reports it:
// a core that is not running or keeps restarting, memory running out, a
// clock that drifts, a certificate about to expire.
func (r *Reporter) Diagnostics(ctx context.Context, s domain.Server, m agentproto.Metrics, d agentproto.Diagnostics) {
	if r == nil {
		return
	}
	now := r.Store.Now().In(r.Store.Location())
	memory := func() string {
		if m.MemTotal <= 0 {
			return ""
		}
		text := fmt.Sprintf("内存 %s / %s", size(m.MemUsed), size(m.MemTotal))
		if m.SwapTotal > 0 {
			text += fmt.Sprintf("，交换分区 %s / %s", size(m.SwapUsed), size(m.SwapTotal))
		}
		return text + "。"
	}

	cores := map[string]bool{}
	for _, c := range d.Cores {
		k := key("core", "server", s.ID, c.Name)
		cores[k] = true
		var running func(domain.Incident) notice
		if c.Active {
			running = func(inc domain.Incident) notice {
				return notice{title: fmt.Sprintf("🟢 %s 上的 %s 恢复运行", s.Name, c.Name), body: []string{fmt.Sprintf("停了%s。", span(now.Sub(inc.OpenedAt)))}}
			}
		}
		r.track(ctx, k, c.Wanted && !c.Active, func() notice {
			n := notice{title: fmt.Sprintf("🔴 %s 上的 %s 没在运行", s.Name, c.Name)}
			if c.LastError != "" {
				n.body = append(n.body, "原因："+code(c.LastError))
			}
			if sc, err := r.look(ctx); err == nil {
				n.body = append(n.body, sc.routes("经过这台的线路现在不通：", s.ID)...)
			}
			return n
		}, running)

		if delta, tell := r.restarted(key("restarts", "server", s.ID, c.Name), c.NRestarts); tell {
			n := notice{title: fmt.Sprintf("🟠 %s 上的 %s 又自己重启了 %d 次", s.Name, c.Name, delta)}
			n.body = append(n.body, fmt.Sprintf("这次开机以来累计 %d 次。每重启一次，经过这台的连接都会断一下。", c.NRestarts))
			if c.LastError != "" {
				n.body = append(n.body, "最近的报错："+code(c.LastError))
			}
			if text := memory(); text != "" {
				n.body = append(n.body, text)
			}
			r.once(ctx, key("restarts", "server", s.ID, c.Name), n)
		}
	}
	// A core the agent no longer reports is no longer wanted there.
	for _, k := range r.openUnder(key("core", "server", s.ID) + "/") {
		if !cores[k] {
			r.end(ctx, k)
		}
	}

	r.track(ctx, key("oom", "server", s.ID), d.OOMEvents > 0, func() notice {
		n := notice{title: "🟠 " + s.Name + " 内存不够用，系统杀过进程"}
		n.body = append(n.body, fmt.Sprintf("过去 24 小时 %d 次。", d.OOMEvents))
		if text := memory(); text != "" {
			n.body = append(n.body, text)
		}
		return n
	}, nil)

	// The skew is measured over the network: a band between the two limits
	// keeps a clock near the line from being told again and again.
	skew := time.Duration(d.ClockSkewMs) * time.Millisecond
	if skew < 0 {
		skew = -skew
	}
	if skew > 5*time.Second || skew < 2*time.Second {
		r.track(ctx, key("clock", "server", s.ID), skew > 5*time.Second, func() notice {
			return notice{title: fmt.Sprintf("🟠 %s 的时钟差了 %.0f 秒", s.Name, skew.Seconds()), body: []string{
				"和主控的时间对不上。Shadowsocks 2022 的中转要求两端相差 30 秒以内，超过了经过它的中转线路会全断；Reality 握手也看时间。",
				"容器的时钟跟宿主机走，虚拟机检查一下时间同步服务。",
			}}
		}, func(inc domain.Incident) notice {
			return notice{title: "🟢 " + s.Name + " 的时钟对上了", body: []string{fmt.Sprintf("前后偏了%s。", span(now.Sub(inc.OpenedAt)))}}
		})
	}

	certs := map[string]bool{}
	for _, c := range d.Certs {
		k := key("cert", "server", s.ID, c.Domain)
		certs[k] = true
		r.track(ctx, k, !c.NotAfter.IsZero() && c.NotAfter.Sub(now) < 7*24*time.Hour, func() notice {
			if c.NotAfter.Before(now) {
				return notice{title: fmt.Sprintf("🔴 %s 的证书 %s 过期了", s.Name, c.Domain), body: []string{"到期时间 " + at(now, c.NotAfter) + "。"}}
			}
			return notice{title: fmt.Sprintf("🟠 %s 的证书 %s 快到期了", s.Name, c.Domain), body: []string{fmt.Sprintf("%s 到期，%s。", date(now, c.NotAfter), inDays(now, c.NotAfter))}}
		}, nil)
	}
	for _, k := range r.openUnder(key("cert", "server", s.ID) + "/") {
		if !certs[k] {
			r.end(ctx, k)
		}
	}
}

// restarted reports how many automatic restarts of a core are new since they
// were last told, once there are enough of them to mean trouble. The count a
// core has when the panel first sees it is where counting starts.
func (r *Reporter) restarted(k string, count int) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	base, seen := r.restarts[k]
	if !seen || count < base {
		r.restarts[k] = count
		return 0, false
	}
	if count-base < 5 {
		return 0, false
	}
	r.restarts[k] = count
	return count - base, true
}

// Applied tells when a server could not take the configuration it was sent,
// and when it has taken one again. failure is empty for a success.
func (r *Reporter) Applied(ctx context.Context, s domain.Server, revision int64, failure string) {
	if r == nil {
		return
	}
	now := r.Store.Now()
	r.track(ctx, key("apply", "server", s.ID), failure != "", func() notice {
		n := notice{title: "🔴 " + s.Name + " 配置下发失败"}
		n.body = append(n.body, fmt.Sprintf("第 %d 版配置没有生效：", revision), code(failure), "这之后在面板里改的入站、线路、用户，在这台机器上都还没生效。面板 → 服务器 → "+esc(s.Name)+" 里有诊断，可以重试。")
		return n
	}, func(inc domain.Incident) notice {
		return notice{title: "🟢 " + s.Name + " 配置下发成功", body: []string{fmt.Sprintf("第 %d 版已生效，前后卡了%s。", revision, span(now.Sub(inc.OpenedAt)))}}
	})
}

// Externals tells when an external subscription stops syncing and when it
// syncs again.
func (r *Reporter) Externals(ctx context.Context) {
	if r == nil {
		return
	}
	exts, err := r.Store.ListExternal(ctx)
	if err != nil {
		return
	}
	now := r.Store.Now()
	for _, e := range exts {
		// A feed that was switched off did not recover.
		var synced func(domain.Incident) notice
		if e.Enabled {
			synced = func(inc domain.Incident) notice {
				return notice{title: "🟢 外部订阅「" + e.Name + "」同步恢复", body: []string{fmt.Sprintf("中断了%s。", span(now.Sub(inc.OpenedAt)))}}
			}
		}
		r.track(ctx, key("sync", "external", e.ID), e.Enabled && e.LastError != "", func() notice {
			return notice{title: "🟠 外部订阅「" + e.Name + "」同步失败", body: []string{code(e.LastError), "它带来的节点保持上次同步时的样子。面板 → 节点 → 外部节点 里可以手动同步。"}}
		}, synced)
	}
}
