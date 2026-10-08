package report

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"ctlvps/internal/domain"
	"ctlvps/internal/store"
)

// ---- what a server costs and when it runs out ----

var cycleNames = map[string]string{"month": "月", "quarter": "季", "half": "半年", "year": "年", "2year": "两年", "3year": "三年"}

// price writes what a server costs, "$22.00/年"; empty when not entered.
func price(s domain.Server) string {
	if s.Price <= 0 {
		return ""
	}
	text := s.Currency + strconv.FormatFloat(s.Price, 'f', 2, 64)
	if name := cycleNames[s.Cycle]; name != "" {
		return text + "/" + name
	}
	return text
}

// Renewals tells when a server is about to run out and when its day has
// passed, and moves the day on for a server that renews by itself.
func (r *Reporter) Renewals(ctx context.Context) error {
	if r == nil {
		return nil
	}
	servers, err := r.Store.ListServers(ctx)
	if err != nil {
		return err
	}
	now := r.Store.Now().In(r.Store.Location())
	ahead := r.Store.GetSettingInt(ctx, domain.SettingRenewAlertDays, 7)
	for _, s := range servers {
		soon, lapsed := key("renew", "server", s.ID), key("lapsed", "server", s.ID)
		due, err := time.ParseInLocation(time.DateOnly, s.ExpiresAt, now.Location())
		if s.ExpiresAt == "" || err != nil {
			r.end(ctx, soon)
			r.end(ctx, lapsed)
			continue
		}
		if months := domain.RenewalCycles[s.Cycle]; s.AutoRenew && months > 0 && daysUntil(now, due) < 0 {
			for daysUntil(now, due) < 0 {
				due = due.AddDate(0, months, 0)
			}
			s.ExpiresAt = due.Format(time.DateOnly)
			if err := r.Store.SetServerExpiry(ctx, s.ID, s.ExpiresAt); err != nil {
				return err
			}
		}
		left := daysUntil(now, due)
		where := "面板 → 服务器 → " + esc(s.Name) + " → 编辑"
		cost := price(s)

		if left >= 0 && left <= ahead {
			r.track(ctx, soon, true, func() notice {
				if s.AutoRenew {
					n := notice{title: fmt.Sprintf("⏳ %s %s自动续费", s.Name, inDays(now, due))}
					line := "到期日 " + s.ExpiresAt + "。"
					if cost != "" {
						line += "会扣 " + esc(cost) + "，"
					}
					n.body = append(n.body, line+"看一眼付款方式还够不够。到了那天面板自己把到期日顺延一期。")
					return n
				}
				n := notice{title: fmt.Sprintf("⏳ %s %s到期", s.Name, inDays(now, due))}
				line := "到期日 " + s.ExpiresAt + "。"
				if cost != "" {
					line += "续费 " + esc(cost) + "。"
				}
				n.body = append(n.body, line, "续完到 "+where+"，把到期日改到下一期。")
				return n
			}, nil)
		} else if inc, ok := r.end(ctx, soon); ok && left > ahead && !s.AutoRenew {
			r.post(ctx, notice{title: "🟢 " + s.Name + " 续上了", body: []string{"新的到期日 " + s.ExpiresAt + "，" + inDays(now, due) + "。"}}, inc.MessageID)
		}

		r.track(ctx, lapsed, left < 0, func() notice {
			n := notice{title: "⛔ " + s.Name + " 的到期日过了", since: due.AddDate(0, 0, 1)}
			n.body = append(n.body, fmt.Sprintf("到期日是 %s，已经过了 %d 天。", s.ExpiresAt, -left))
			n.body = append(n.body, "已经续了："+where+"，把到期日改到下一期。没续的话，服务商随时可能收回这台机器。")
			return n
		}, func(domain.Incident) notice {
			return notice{title: "🟢 " + s.Name + " 的到期日更新了", body: []string{"新的到期日 " + s.ExpiresAt + "，" + inDays(now, due) + "。"}}
		})
	}
	return nil
}

// ---- how good the way from an entry server to a probe target is ----

// What counts as bad, and as good again. Between the two nothing changes:
// a way that hovers at the edge is told once.
const (
	qualityWindow    = 10 // minutes judged
	qualityMinProbes = 12 // fewer in the window says too little
	lossBad          = 0.20
	lossGood         = 0.05
	slowBad          = 1.8 // times the usual round trip
	slowBadBy        = 80 * time.Millisecond
	slowGood         = 1.3
	deadLoss         = 0.95
)

// QualityWindow is how many finished minutes Quality wants summed.
const QualityWindow = qualityWindow

type way struct {
	server domain.Server
	target domain.ProbeTarget
	seen   store.ProbePoint
	usual  int64 // microseconds, 0 when not known yet
}

func ms(us int64) string { return strconv.FormatInt((us+500)/1000, 10) + " ms" }

func (w way) loss() float64 { return float64(w.seen.Lost) / float64(w.seen.Sent) }

func (w way) name() string { return esc(w.server.Name) + " → " + esc(w.target.Name) }

// state writes how the way stands: its loss, and its round trip when
// anything answered.
func (w way) state() string {
	text := "丢包 " + percent(w.loss()*100)
	if avg := w.seen.Avg(); avg > 0 {
		text += "，延迟 " + ms(avg)
		if w.usual > 0 {
			text += "（平时 " + ms(w.usual) + "）"
		}
	}
	return text
}

func (w way) slow(factor float64, by time.Duration) bool {
	avg := w.seen.Avg()
	return w.usual > 0 && avg > 0 && float64(avg) >= factor*float64(w.usual) && avg-w.usual >= by.Microseconds()
}

// Quality tells when the way from an entry server to a probe target loses
// packets or takes much longer than it usually does, and when it is good
// again. window is what each server's probes of each target came to over the
// last QualityWindow minutes. Only entry servers are judged: they are what
// users connect to. A target that many servers lose at once is the target's
// own trouble and is told as that, once.
func (r *Reporter) Quality(ctx context.Context, window map[store.ProbeKey]store.ProbePoint) error {
	if r == nil {
		return nil
	}
	sc, err := r.look(ctx)
	if err != nil {
		return err
	}
	list, err := r.Store.ListProbeTargets(ctx)
	if err != nil {
		return err
	}
	targets := map[int64]domain.ProbeTarget{}
	for _, t := range list {
		targets[t.ID] = t
	}
	on := r.Store.GetSettingBool(ctx, domain.SettingTelegramQuality, true)

	// What can no longer be judged ends without a word.
	for _, k := range r.openUnder("quality/") {
		_, _, id := about(k)
		target, _ := strconv.ParseInt(strings.TrimPrefix(k[strings.LastIndex(k, "/")+1:], "t"), 10, 64)
		if _, ok := targets[target]; !on || !ok || !sc.entry[id] {
			r.end(ctx, k)
		}
	}
	for _, k := range r.openUnder("probe/") {
		if _, _, id := about(k); !on || targets[id].ID == 0 {
			r.end(ctx, k)
		}
	}
	if !on {
		return nil
	}

	if r.Store.Now().Sub(r.usualAt) > 10*time.Minute {
		usual, err := r.Store.ProbeBaselines(ctx, r.Store.Now().Add(-72*time.Hour), 6)
		if err != nil {
			return err
		}
		r.usual, r.usualAt = usual, r.Store.Now()
	}

	// A target most servers cannot reach has stopped answering itself.
	dead := map[int64]bool{}
	for _, t := range list {
		probing, unreachable := 0, 0
		for k, p := range window {
			if k.TargetID == t.ID && p.Sent >= qualityMinProbes {
				probing++
				if float64(p.Lost)/float64(p.Sent) >= deadLoss {
					unreachable++
				}
			}
		}
		dead[t.ID] = probing >= 3 && unreachable*10 >= probing*7
		k := key("probe", "target", t.ID)
		if probing == 0 {
			continue
		}
		r.track(ctx, k, dead[t.ID], func() notice {
			return notice{title: "⚠️ 探测目标 " + t.Name + " 连不上了", body: []string{
				fmt.Sprintf("%d 台服务器里有 %d 台同时连不上 %s:%d，多半是这个目标自己停了，不是线路的问题。", probing, unreachable, esc(t.Host), t.Port),
				"它恢复之前，这个目标的延迟和丢包不再提醒。一直不恢复就到 面板 → 服务器 → 延迟监测 里换一个。",
			}}
		}, func(inc domain.Incident) notice {
			return notice{title: "🟢 探测目标 " + t.Name + " 恢复了", body: []string{"停了" + span(sc.now.Sub(inc.OpenedAt)) + "。"}}
		})
	}

	var worse []way
	var better []struct {
		way
		inc domain.Incident
	}
	// A server that stopped reporting has nothing in the window: what was
	// said about it stands until its probes are back to say otherwise.
	for _, s := range sc.servers {
		if !sc.entry[s.ID] {
			continue
		}
		for _, t := range list {
			seen, ok := window[store.ProbeKey{ServerID: s.ID, TargetID: t.ID}]
			if !ok || seen.Sent < qualityMinProbes || dead[t.ID] {
				continue
			}
			w := way{server: s, target: t, seen: seen, usual: r.usual[store.ProbeKey{ServerID: s.ID, TargetID: t.ID}]}
			k := key("quality", "server", s.ID, "t"+strconv.FormatInt(t.ID, 10))
			switch {
			case w.loss() >= lossBad || w.slow(slowBad, slowBadBy):
				if !r.isOpen(k) {
					worse = append(worse, w)
				}
			case w.loss() < lossGood && !w.slow(slowGood, 0):
				if inc, ok := r.end(ctx, k); ok {
					better = append(better, struct {
						way
						inc domain.Incident
					}{w, inc})
				}
			}
		}
	}

	if len(worse) > 0 {
		sort.SliceStable(worse, func(i, j int) bool { return worse[i].loss() > worse[j].loss() })
		var n notice
		if len(worse) == 1 {
			w := worse[0]
			what := "丢包 " + percent(w.loss()*100)
			if w.loss() < lossBad {
				what = "延迟升到 " + ms(w.seen.Avg())
			}
			n = notice{title: fmt.Sprintf("📶 %s 到%s %s", w.server.Name, w.target.Name, what)}
			n.body = append(n.body, fmt.Sprintf("近 %d 分钟：%s。", qualityWindow, w.state()))
			if hit := sc.through(w.server.ID); len(hit) > 0 {
				n.body = append(n.body, "从"+esc(w.target.Name)+"连这些线路会卡："+esc(strings.Join(hit, "、")))
			}
		} else {
			n = notice{title: fmt.Sprintf("📶 %d 条入口到国内的路变差了", len(worse))}
			for _, w := range worse {
				n.body = append(n.body, "· "+w.name()+"："+w.state())
			}
			n.body = append(n.body, fmt.Sprintf("都是近 %d 分钟的数。", qualityWindow))
		}
		n.body = append(n.body, "线路还连得上，只是慢或者卡；持续的话换一条入口不同的线路。恢复了会再说一声。")
		var began []domain.Incident
		for _, w := range worse {
			title := "📶 " + w.server.Name + " 到" + w.target.Name + "变差"
			if inc, ok := r.begin(ctx, key("quality", "server", w.server.ID, "t"+strconv.FormatInt(w.target.ID, 10)), title, time.Time{}); ok {
				began = append(began, inc)
			}
		}
		if len(began) > 0 {
			message := r.post(ctx, n, 0)
			for _, inc := range began {
				r.attach(ctx, inc, message)
			}
		}
	}

	if len(better) > 0 {
		answer := better[0].inc.MessageID
		for _, b := range better {
			if b.inc.MessageID != answer {
				answer = 0
			}
		}
		var n notice
		if len(better) == 1 {
			b := better[0]
			n = notice{title: fmt.Sprintf("🟢 %s 到%s恢复正常", b.server.Name, b.target.Name), body: []string{
				fmt.Sprintf("差了%s。现在%s。", span(sc.now.Sub(b.inc.OpenedAt)), b.state()),
			}}
		} else {
			n = notice{title: fmt.Sprintf("🟢 %d 条入口到国内的路恢复正常", len(better))}
			for _, b := range better {
				n.body = append(n.body, fmt.Sprintf("· %s：差了%s，现在%s", b.name(), span(sc.now.Sub(b.inc.OpenedAt)), b.state()))
			}
		}
		r.post(ctx, n, answer)
	}
	return nil
}
