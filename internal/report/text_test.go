package report

import (
	"testing"
	"time"
)

func TestFiguresReadTheWayThePanelWritesThem(t *testing.T) {
	for b, want := range map[int64]string{0: "0 B", 1000: "1000 B", 1536: "1.50 KB", 81 << 30: "81.0 GB", 480 << 30: "480 GB", 1 << 40: "1.00 TB"} {
		if got := size(b); got != want {
			t.Errorf("size(%d) = %q, want %q", b, got, want)
		}
	}
	for d, want := range map[time.Duration]string{20 * time.Second: "不到 1 分钟", 12 * time.Minute: " 12 分钟", 3 * time.Hour: " 3 小时", 185 * time.Minute: " 3 小时 5 分钟", 52 * time.Hour: " 2 天 4 小时"} {
		if got := span(d); got != want {
			t.Errorf("span(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestOutlookSaysWhetherTheQuotaLasts(t *testing.T) {
	shanghai := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 19, 21, 0, 0, 0, shanghai)
	reset := time.Date(2026, 10, 28, 0, 0, 0, 0, shanghai)
	const gb = 1 << 30
	// 120 GB left at 21 GB a day: gone on the 25th, three days short.
	if text, short := outlook(now, 120*gb, 21*gb, &reset); !short || text != "近 7 天日均 21.0 GB，照这个速度约 10-25 用完，比重置早 3 天。" {
		t.Errorf("short: %v %q", short, text)
	}
	if text, short := outlook(now, 120*gb, 10*gb, &reset); short || text != "近 7 天日均 10.0 GB，照这个速度够用到重置。" {
		t.Errorf("lasting: %v %q", short, text)
	}
	if text, short := outlook(now, 120*gb, 21*gb, nil); short || text != "近 7 天日均 21.0 GB，照这个速度约 10-25 用完。" {
		t.Errorf("no reset: %v %q", short, text)
	}
	if text, _ := outlook(now, 120*gb, 0, &reset); text != "" {
		t.Errorf("nothing carried, nothing to say: %q", text)
	}
	for when, want := range map[time.Time]string{now.Add(time.Hour): "今天", now.Add(4 * time.Hour): "明天", reset: "还有 9 天"} {
		if got := inDays(now, when); got != want {
			t.Errorf("inDays(%v) = %q, want %q", when, got, want)
		}
	}
}
