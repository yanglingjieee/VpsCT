package report

import (
	"context"
	"fmt"
	"sort"
	"strconv"
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
var lasting = map[string]bool{"offline": true, "core": true, "stop": true, "out": true, "expired": true, "apply": true, "sync": true, "clock": true, "quality": true, "probe": true, "lapsed": true}

// quiet is a day's traffic too small to list, keep-alives and latency tests,
// and how the report names it.
const (
	quiet     = 10 << 20
	quietText = "10 MB"
)

// daily writes the report of a day for a phone: a line for how the network
// stands, who used it and over which lines, how each server stands against
// its quota, how far the entries were from the country, what will run short,
// and what happened. It leaves out what needs no attention. Sent in the
// morning it covers the day before; later, the day so far.
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
	var short []string // what runs out before its reset, and what is close to full

	// ---- users ----
	// Most first. A user is listed for what they carried that day, or for
	// being past half of their quota; the rest share one line.
	type person struct {
		day  int64
		rows []string
	}
	var listed []person
	var idle []string
	for _, sh := range shares {
		if sh.Status == domain.ShareRevoked {
			continue
		}
		used := userTotal(sh.UsedUpload, sh.UsedDownload)
		day := carried(store.SubjectShare, []int64{sh.ID}, userTotal)
		var reset *time.Time
		if sh.ResetDay > 0 {
			next := traffic.NextReset(now, sh.ResetDay)
			reset = &next
		}
		row, half := "<b>"+esc(sh.Name)+"</b>　"+size(day), false
		if sh.QuotaBytes > 0 {
			if pct := float64(used) / float64(sh.QuotaBytes) * 100; pct >= 50 {
				row, half = row+" · 本期 "+percent(pct), true
			}
		}
		switch sh.Status {
		case domain.ShareExhausted:
			row += " · ⛔ 已用完"
		case domain.ShareExpired:
			row += " · ⛔ 已到期"
		case domain.SharePaused:
			row += " · ⏸ 已暂停"
		}
		if sh.Status == domain.ShareActive && sh.QuotaBytes > 0 {
			if text, yes := outlook(now, sh.QuotaBytes-used, r.pace(ctx, store.SubjectShare, sh.ID, userTotal), reset); yes {
				short = append(short, "· "+esc(sh.Name)+"："+text)
			}
		}
		if day < quiet && !half {
			idle = append(idle, esc(sh.Name))
			continue
		}
		listed = append(listed, person{day, append([]string{row}, r.byLine(ctx, sc, sh.ID, day, from, to)...)})
	}
	sort.SliceStable(listed, func(i, j int) bool { return listed[i].day > listed[j].day })
	people := []string{"<b>👤 用户 · " + label + "</b>"}
	for _, p := range listed {
		people = append(people, p.rows...)
	}
	switch {
	case len(idle) > 6:
		people = append(people, fmt.Sprintf("另有 %d 人不到 %s", len(idle), quietText))
	case len(idle) > 0:
		people = append(people, "不到 "+quietText+"："+strings.Join(idle, "、"))
	}

	// ---- servers ----
	// A server is listed when lines run through it, when it has a quota, or
	// when it is in trouble; one that is only watched and well says nothing.
	machines := []string{"<b>🖥 服务器 · " + label + "</b>"}
	total, online := 0, 0
	for _, s := range sc.servers {
		state := ""
		switch {
		case !s.Enabled:
			state = "⚪ 已停用"
		case !sc.enrolled(s.ID):
			state = "⚪ 还没接入"
		case sc.lost(s.ID):
			state = "🔴 失联"
		case s.QuotaStopped:
			state = "⛔ 入站已停"
		}
		if s.Enabled && sc.enrolled(s.ID) {
			total++
			if !sc.lost(s.ID) {
				online++
			}
		}
		row, limited := "<b>"+esc(s.Name)+"</b>　"+size(carried(store.SubjectServer, []int64{s.ID}, serverBilled(s))), false
		if u, err := r.Traffic.ServerUsage(ctx, s); err == nil && u.Quota > 0 {
			row, limited = row+" · 本期 "+percent(u.Percent), true
			if !u.OverQuota {
				if text, yes := outlook(now, u.Quota-u.Billed, r.pace(ctx, store.SubjectServer, s.ID, serverBilled(s)), u.NextReset); yes {
					short = append(short, "· "+esc(s.Name)+"："+text)
				}
			}
		}
		if state != "" {
			row += " · " + state
		} else if hot := strained(sc.metrics(s.ID)); len(hot) > 0 {
			short = append(short, "· "+esc(s.Name)+"："+strings.Join(hot, "、"))
		}
		if len(sc.through(s.ID)) > 0 || limited || sc.lost(s.ID) || s.QuotaStopped {
			machines = append(machines, row)
		}
	}

	// ---- the way in ----
	// Users connect to the entry servers from inside the country, so the
	// report says how far each was from the targets shown on the cards: the
	// round trips in the order of the heading, and the loss where there was any.
	targets, err := r.Store.ListProbeTargets(ctx)
	if err != nil {
		return "", err
	}
	probes, err := r.Store.ProbeStats(ctx, 0, store.LiveHour, from.Truncate(time.Hour), to, 1<<40)
	if err != nil {
		return "", err
	}
	var cards []domain.ProbeTarget
	var heading []string
	for _, t := range targets {
		if t.OnCard {
			cards, heading = append(cards, t), append(heading, esc(t.Name))
		}
	}
	way := []string{"<b>📶 入口到国内</b>（" + strings.Join(heading, " / ") + "）"}
	for _, s := range sc.servers {
		if !sc.entry[s.ID] {
			continue
		}
		trips, answered, probed := make([]string, len(cards)), false, false
		var lossy []string
		for i, t := range cards {
			trips[i] = "—"
			for _, p := range probes {
				if p.ServerID != s.ID || p.TargetID != t.ID || p.Sent == 0 {
					continue
				}
				probed = true
				if p.Sent == p.Lost {
					trips[i] = "不通"
					continue
				}
				trips[i], answered = strconv.FormatInt((p.Avg()+500)/1000, 10), true
				if loss := float64(p.Lost) / float64(p.Sent) * 100; loss >= 1 {
					lossy = append(lossy, esc(t.Name)+" "+percent(loss))
				}
			}
		}
		if !probed {
			continue
		}
		row := "<b>" + esc(s.Name) + "</b>　" + strings.Join(trips, " / ")
		if answered {
			row += " ms"
		}
		way = append(way, row)
		if len(lossy) > 0 {
			way = append(way, "　丢包："+strings.Join(lossy, "、"))
		}
	}

	// ---- what happened ----
	past, err := r.Store.IncidentsBetween(ctx, from, to)
	if err != nil {
		return "", err
	}
	happened := []string{"<b>🗓 " + label + "发生的事</b>"}
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
	unresolved := []string{"<b>⏳ 还没解决</b>"}
	for _, inc := range pending {
		unresolved = append(unresolved, fmt.Sprintf("· %s（%s 起，已经%s）", esc(inc.Title), at(now, inc.OpenedAt), span(now.Sub(inc.OpenedAt))))
	}

	// ---- the report ----
	site := r.Store.GetSetting(ctx, domain.SettingSiteName, "土豆饼的家")
	usable := len(sc.working())
	head := fmt.Sprintf("%d 台服务器 %d 台在线", total, online)
	if total == online {
		head = fmt.Sprintf("%d 台服务器都在线", total)
	}
	switch {
	case len(sc.lines) == 0:
		head += " · 还没有线路"
	case usable == len(sc.lines):
		head += fmt.Sprintf(" · %d 条线路都可用", usable)
	default:
		head += fmt.Sprintf(" · %d 条线路 %d 条可用", len(sc.lines), usable)
	}
	mark := "✅ "
	if total != online || usable != len(sc.lines) || len(pending) > 0 {
		mark = "⚠️ "
	}
	if len(pending) > 0 {
		head += fmt.Sprintf(" · %d 件事还没解决", len(pending))
	}
	sections := [][]string{
		{fmt.Sprintf("📊 <b>%s · %s %s 日报</b>", esc(site), from.Format("01-02"), weekdays[from.Weekday()]), mark + head},
	}
	for _, section := range [][]string{people, machines, way} {
		if len(section) > 1 {
			sections = append(sections, section)
		}
	}
	if len(short) > 0 {
		sections = append(sections, append([]string{"<b>📌 要留意</b>"}, short...))
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

// byLine is the lines a user's day went over, one to a row, most first:
// those that carried a twentieth of the day or more, three at the most. A
// line is counted on both machines of a relay the way the user's quota is;
// one that carried nearly all of the day goes without its figure.
func (r *Reporter) byLine(ctx context.Context, sc *scene, shareID, day int64, from, to time.Time) []string {
	nodes, err := r.Store.ListNodes(ctx, store.NodeFilter{ShareID: &shareID, IncludeRevoked: true})
	if err != nil {
		return nil
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
		if err != nil || (up+down)*20 < day || up+down == 0 {
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
	if len(uses) > 3 {
		uses = uses[:3]
	}
	rows := make([]string, 0, len(uses))
	for _, u := range uses {
		row := "　" + esc(u.name)
		if len(uses) > 1 || u.bytes*20 < day*19 {
			row += " " + size(u.bytes)
		}
		rows = append(rows, row)
	}
	return rows
}
