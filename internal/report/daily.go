package report

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"ctlvps/internal/domain"
	"ctlvps/internal/notify"
	"ctlvps/internal/store"
	"ctlvps/internal/traffic"
)

// DailyDue posts the daily report once a day, in the hour the operator
// chose, on the panel's clock.
func (r *Reporter) DailyDue(ctx context.Context) error {
	if r == nil || !r.Store.GetSettingBool(ctx, domain.SettingTelegramDaily, false) {
		return nil
	}
	now := r.Store.Now().In(r.Store.Location())
	today := now.Format("2006-01-02")
	if now.Hour() != r.Store.GetSettingInt(ctx, domain.SettingTelegramHour, 9) || r.Store.GetSetting(ctx, domain.SettingTelegramSent, "") == today {
		return nil
	}
	// A day whose report cannot be delivered is not retried every ten
	// minutes: the settings page shows what is wrong.
	if err := r.Store.SetSetting(ctx, domain.SettingTelegramSent, today); err != nil {
		return err
	}
	return r.PostDaily(ctx)
}

// PostDaily posts the daily report now.
func (r *Reporter) PostDaily(ctx context.Context) error {
	if r == nil || r.Telegram == nil {
		return notify.ErrNotConfigured
	}
	text, err := r.daily(ctx)
	if err != nil {
		return err
	}
	_, err = r.Telegram.Post(ctx, text, 0)
	return err
}

var weekdays = [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

// lasting are the kinds of incident whose length is worth telling.
var lasting = map[string]bool{"offline": true, "core": true, "stop": true, "out": true, "expired": true, "apply": true, "sync": true, "clock": true}

// daily writes the report of a day: who used how much and over which lines,
// how every server stands against its quota, what will run short, and what
// happened. Sent in the morning it covers the day before; later, the day so
// far.
func (r *Reporter) daily(ctx context.Context) (string, error) {
	sc, err := r.look(ctx)
	if err != nil {
		return "", err
	}
	now := sc.now
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	label, from, to := "今天", midnight, now
	if now.Hour() < 12 {
		label, from, to = "昨天", midnight.AddDate(0, 0, -1), midnight
	}
	shares, err := r.Store.ListShares(ctx, nil)
	if err != nil {
		return "", err
	}
	carried := func(subject string, ids []int64, per func(up, down int64) int64) int64 {
		up, down, err := r.Store.SumHourly(ctx, subject, ids, from, to)
		if err != nil {
			return 0
		}
		return per(up, down)
	}
	var short []string // what runs out before its reset

	// ---- users ----
	people := []string{"<b>用户</b>"}
	for _, sh := range shares {
		if sh.Status == domain.ShareRevoked {
			continue
		}
		used := userTotal(sh.UsedUpload, sh.UsedDownload)
		line := fmt.Sprintf("<b>%s</b>　%s %s · 本期 %s", esc(sh.Name), label, size(carried(store.SubjectShare, []int64{sh.ID}, userTotal)), size(used))
		var reset *time.Time
		if sh.ResetDay > 0 {
			next := traffic.NextReset(now, sh.ResetDay)
			reset = &next
		}
		if sh.QuotaBytes > 0 {
			line += fmt.Sprintf(" / %s（%s）", size(sh.QuotaBytes), percent(float64(used)/float64(sh.QuotaBytes)*100))
		} else {
			line += "（不限量）"
		}
		switch sh.Status {
		case domain.ShareExhausted:
			line += " · ⛔ 已用完"
		case domain.ShareExpired:
			line += " · ⛔ 已到期"
		case domain.SharePaused:
			line += " · ⏸ 已暂停"
		}
		if reset != nil {
			line += " · " + date(now, *reset) + " 重置"
		}
		people = append(people, line)
		if via := r.byLine(ctx, sc, sh.ID, from, to); via != "" {
			people = append(people, "　└ "+via)
		}
		if sh.Status == domain.ShareActive && sh.QuotaBytes > 0 {
			if text, yes := outlook(now, sh.QuotaBytes-used, r.pace(ctx, store.SubjectShare, sh.ID, userTotal), reset); yes {
				short = append(short, "· "+esc(sh.Name)+"："+text)
			}
		}
	}

	// ---- servers ----
	carrying, watching := []string{"<b>服务器</b>"}, []string{"<b>只做监控</b>"}
	total, online := 0, 0
	for _, s := range sc.servers {
		mark, state := "🟢", ""
		switch {
		case !s.Enabled:
			mark, state = "⚪", "已停用"
		case !sc.enrolled(s.ID):
			mark, state = "⚪", "还没接入"
		case sc.lost(s.ID):
			mark, state = "🔴", "失联"
		case s.QuotaStopped:
			mark, state = "⛔", "入站已停"
		}
		if s.Enabled && sc.enrolled(s.ID) {
			total++
			if !sc.lost(s.ID) {
				online++
			}
		}
		line := fmt.Sprintf("%s <b>%s</b>　%s %s", mark, esc(s.Name), label, size(carried(store.SubjectServer, []int64{s.ID}, serverBilled(s))))
		if u, err := r.Traffic.ServerUsage(ctx, s); err == nil {
			line += " · 本期 " + size(u.Billed)
			if u.Quota > 0 {
				line += fmt.Sprintf(" / %s（%s）", size(u.Quota), percent(u.Percent))
			}
			if u.NextReset != nil {
				line += " · " + date(now, *u.NextReset) + " 重置"
			}
			if u.Quota > 0 && !u.OverQuota {
				if text, yes := outlook(now, u.Quota-u.Billed, r.pace(ctx, store.SubjectServer, s.ID, serverBilled(s)), u.NextReset); yes {
					short = append(short, "· "+esc(s.Name)+"："+text)
				}
			}
		}
		if state != "" {
			line += " · " + state
		} else if hot := strained(sc.metrics(s.ID)); len(hot) > 0 {
			line += " · ⚠️ " + strings.Join(hot, "、")
		}
		if len(sc.through(s.ID)) > 0 {
			carrying = append(carrying, line)
		} else {
			watching = append(watching, line)
		}
	}

	// ---- what happened ----
	past, err := r.Store.IncidentsBetween(ctx, from, to)
	if err != nil {
		return "", err
	}
	happened := []string{"<b>" + label + "发生的事</b>"}
	for _, inc := range past {
		if inc.ResolvedAt == nil {
			continue // listed below as still open
		}
		line := "· " + inc.OpenedAt.In(now.Location()).Format("15:04") + " " + esc(inc.Title)
		if kind, _, _ := about(inc.Key); lasting[kind] {
			line += "，持续" + span(inc.ResolvedAt.Sub(inc.OpenedAt))
		}
		happened = append(happened, line)
	}
	r.mu.Lock()
	pending := make([]domain.Incident, 0, len(r.open))
	for _, inc := range r.open {
		pending = append(pending, inc)
	}
	r.mu.Unlock()
	sort.Slice(pending, func(i, j int) bool { return pending[i].OpenedAt.Before(pending[j].OpenedAt) })
	unresolved := []string{"<b>还没解决</b>"}
	for _, inc := range pending {
		unresolved = append(unresolved, fmt.Sprintf("· %s（%s 起，已经%s）", esc(inc.Title), at(now, inc.OpenedAt), span(now.Sub(inc.OpenedAt))))
	}

	// ---- the report ----
	site := r.Store.GetSetting(ctx, domain.SettingSiteName, "土豆饼的家")
	head := fmt.Sprintf("%d 台服务器 %d 台在线", total, online)
	if total == online {
		head = fmt.Sprintf("%d 台服务器都在线", total)
	}
	if usable := len(sc.working()); len(sc.lines) == 0 {
		head += " · 还没有线路"
	} else if usable == len(sc.lines) {
		head += fmt.Sprintf(" · %d 条线路都可用", usable)
	} else {
		head += fmt.Sprintf(" · %d 条线路 %d 条可用", len(sc.lines), usable)
	}
	if len(pending) > 0 {
		head += fmt.Sprintf(" · %d 件事还没解决", len(pending))
	}
	sections := [][]string{
		{fmt.Sprintf("📊 <b>%s · %s %s 日报</b>", esc(site), from.Format("01-02"), weekdays[from.Weekday()]), head},
	}
	for _, section := range [][]string{people, carrying, watching} {
		if len(section) > 1 {
			sections = append(sections, section)
		}
	}
	if len(short) > 0 {
		sections = append(sections, append([]string{"<b>要留意</b>"}, short...))
	}
	if len(happened) > 1 {
		sections = append(sections, happened)
	}
	if len(unresolved) > 1 {
		sections = append(sections, unresolved)
	}
	if len(happened) == 1 && len(unresolved) == 1 {
		sections = append(sections, []string{label + "没有出过状况。"})
	}
	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		parts = append(parts, strings.Join(section, "\n"))
	}
	return strings.Join(parts, "\n\n"), nil
}

// byLine is what a user carried over each line in [from, to), most first,
// counted on both machines of a relay line the way the user's quota is.
func (r *Reporter) byLine(ctx context.Context, sc *scene, shareID int64, from, to time.Time) string {
	nodes, err := r.Store.ListNodes(ctx, store.NodeFilter{ShareID: &shareID, IncludeRevoked: true})
	if err != nil {
		return ""
	}
	members := map[int64][]int64{}
	for _, n := range nodes {
		if n.LineID != nil {
			members[*n.LineID] = append(members[*n.LineID], n.ID)
		}
	}
	type use struct {
		name  string
		bytes int64
	}
	var uses []use
	for line, ids := range members {
		up, down, err := r.Store.SumHourly(ctx, store.SubjectNode, ids, from, to)
		if err != nil || up+down == 0 {
			continue
		}
		uses = append(uses, use{sc.named[line], up + down})
	}
	sort.Slice(uses, func(i, j int) bool {
		if uses[i].bytes != uses[j].bytes {
			return uses[i].bytes > uses[j].bytes
		}
		return uses[i].name < uses[j].name
	})
	parts := make([]string, 0, len(uses))
	for _, u := range uses {
		parts = append(parts, esc(u.name)+" "+size(u.bytes))
	}
	return strings.Join(parts, " · ")
}
