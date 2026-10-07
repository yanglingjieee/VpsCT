package report

import (
	"fmt"
	"html"
	"strings"
	"time"

	"ctlvps/internal/agentproto"
	"ctlvps/internal/domain"
)

func esc(s string) string { return html.EscapeString(s) }

// size writes a byte count the way the panel does: GB / MB on base 1024.
func size(b int64) string {
	if b < 0 {
		b = 0
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	v, exp := float64(b)/unit, 0
	for v >= unit && exp < 4 {
		v /= unit
		exp++
	}
	suffix := "KMGTP"[exp:exp+1] + "B"
	switch {
	case v >= 100:
		return fmt.Sprintf("%.0f %s", v, suffix)
	case v >= 10:
		return fmt.Sprintf("%.1f %s", v, suffix)
	}
	return fmt.Sprintf("%.2f %s", v, suffix)
}

func rate(bytesPerSec int64) string { return size(bytesPerSec) + "/s" }

func percent(p float64) string {
	if p > 0 && p < 10 {
		return fmt.Sprintf("%.1f%%", p)
	}
	return fmt.Sprintf("%.0f%%", p)
}

// span writes how long something lasted, to follow a verb directly: a
// figure brings its own space, words do not ("停了 12 分钟", "停了不到 1 分钟").
func span(d time.Duration) string {
	if d < time.Minute {
		return "不到 1 分钟"
	}
	minutes := int(d / time.Minute)
	days, hours, minutes := minutes/1440, minutes%1440/60, minutes%60
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf(" %d 天 %d 小时", days, hours)
	case days > 0:
		return fmt.Sprintf(" %d 天", days)
	case hours > 0 && minutes > 0:
		return fmt.Sprintf(" %d 小时 %d 分钟", hours, minutes)
	case hours > 0:
		return fmt.Sprintf(" %d 小时", hours)
	}
	return fmt.Sprintf(" %d 分钟", minutes)
}

// daysUntil counts the midnights between now and t: 0 is today.
func daysUntil(now, t time.Time) int {
	y, m, d := now.Date()
	ty, tm, td := t.In(now.Location()).Date()
	a := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	b := time.Date(ty, tm, td, 0, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Hours() / 24)
}

// inDays writes when a date comes, counted in days.
func inDays(now, t time.Time) string {
	switch n := daysUntil(now, t); {
	case n <= 0:
		return "今天"
	case n == 1:
		return "明天"
	default:
		return fmt.Sprintf("还有 %d 天", n)
	}
}

// at writes a moment for a reader on the same day, and with its date for any
// other day.
func at(now, t time.Time) string {
	t = t.In(now.Location())
	if daysUntil(now, t) == 0 {
		return t.Format("15:04")
	}
	return t.Format("01-02 15:04")
}

func date(now, t time.Time) string { return t.In(now.Location()).Format("01-02") }

// billingName says how a server's host counts traffic.
func billingName(mode string) string {
	if mode == domain.BillingOut {
		return "只算出站"
	}
	return "双向都算"
}

// vitals is a server's load in one line, empty before its first report.
func vitals(m agentproto.Metrics) string {
	if m.MemTotal <= 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("CPU %.0f%%", m.CPUPercent), fmt.Sprintf("内存 %.0f%%", float64(m.MemUsed)/float64(m.MemTotal)*100)}
	if m.DiskTotal > 0 {
		parts = append(parts, fmt.Sprintf("硬盘 %.0f%%", float64(m.DiskUsed)/float64(m.DiskTotal)*100))
	}
	parts = append(parts, "↑"+rate(m.NetTxRate)+" ↓"+rate(m.NetRxRate))
	return strings.Join(parts, " · ")
}

// strained names what is close to full on a server, for a report that only
// mentions load when it matters.
func strained(m agentproto.Metrics) []string {
	var out []string
	if m.MemTotal > 0 {
		if p := float64(m.MemUsed) / float64(m.MemTotal) * 100; p >= 90 {
			out = append(out, fmt.Sprintf("内存 %.0f%%", p))
		}
	}
	if m.DiskTotal > 0 {
		if p := float64(m.DiskUsed) / float64(m.DiskTotal) * 100; p >= 85 {
			out = append(out, fmt.Sprintf("硬盘 %.0f%%", p))
		}
	}
	if m.CPUPercent >= 90 {
		out = append(out, fmt.Sprintf("CPU %.0f%%", m.CPUPercent))
	}
	return out
}

// bullets writes names as a list.
func bullets(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, "· "+esc(n))
	}
	return out
}

// code writes machine output so that it stands apart from the sentence,
// shortened to what a message can carry.
func code(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300]) + "…"
	}
	return "<code>" + esc(s) + "</code>"
}
