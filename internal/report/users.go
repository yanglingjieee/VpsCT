package report

import (
	"context"
	"fmt"
	"time"

	"ctlvps/internal/domain"
	"ctlvps/internal/store"
	"ctlvps/internal/traffic"
)

// Users brings what was said about every user in line with how they stand.
func (r *Reporter) Users(ctx context.Context) error {
	if r == nil {
		return nil
	}
	shares, err := r.Store.ListShares(ctx, nil)
	if err != nil {
		return err
	}
	for _, sh := range shares {
		r.User(ctx, sh)
	}
	return nil
}

// userTotal counts a user's traffic the way their quota does.
func userTotal(up, down int64) int64 { return domain.Total(up, down) }

// User tells when a user nears their quota, uses it up, is about to expire
// or has expired, and when their lines carry traffic again. A user the
// operator paused or revoked is the operator's own doing and is not told.
func (r *Reporter) User(ctx context.Context, sh domain.Share) {
	if r == nil {
		return
	}
	now := r.Store.Now().In(r.Store.Location())
	alert := float64(r.Store.GetSettingInt(ctx, domain.SettingQuotaAlertPct, 80))
	live := sh.Status == domain.ShareActive
	used := userTotal(sh.UsedUpload, sh.UsedDownload)
	var pct float64
	if sh.QuotaBytes > 0 {
		pct = float64(used) / float64(sh.QuotaBytes) * 100
	}
	var reset *time.Time
	if sh.ResetDay > 0 {
		next := traffic.NextReset(now, sh.ResetDay)
		reset = &next
	}
	usage := fmt.Sprintf("已用 %s / %s（上传 %s，下载 %s）", size(used), size(sh.QuotaBytes), size(sh.UsedUpload), size(sh.UsedDownload))
	where := "面板 → 用户 → " + esc(sh.Name)
	// Lines only "come back" for a user who is in service again.
	back := func(title string) func(domain.Incident) notice {
		if !live {
			return nil
		}
		return func(inc domain.Incident) notice {
			n := notice{title: title, body: []string{fmt.Sprintf("停了%s。", span(now.Sub(inc.OpenedAt)))}}
			if sh.QuotaBytes > 0 {
				n.body = append(n.body, "本期"+usage+"。")
			}
			return n
		}
	}

	r.track(ctx, key("quota", "user", sh.ID), live && sh.QuotaBytes > 0 && pct >= alert, func() notice {
		n := notice{title: fmt.Sprintf("⚠️ %s 本期流量用到 %s", sh.Name, percent(pct))}
		n.body = append(n.body, usage+"，还剩 "+size(sh.QuotaBytes-used)+"。")
		if reset != nil {
			n.body = append(n.body, fmt.Sprintf("%s 重置，%s。", date(now, *reset), inDays(now, *reset)))
		}
		if text, _ := outlook(now, sh.QuotaBytes-used, r.pace(ctx, store.SubjectShare, sh.ID, userTotal), reset); text != "" {
			n.body = append(n.body, text)
		}
		n.body = append(n.body, "用完后这个用户的线路会自动停。不想断："+where+"，调大限额。")
		return n
	}, nil)

	r.track(ctx, key("out", "user", sh.ID), sh.Status == domain.ShareExhausted, func() notice {
		n := notice{title: "⛔ " + sh.Name + " 本期流量用完了，线路已停"}
		n.body = append(n.body, usage+"。")
		if reset != nil {
			n.body = append(n.body, fmt.Sprintf("%s 重置后自动恢复，%s。", date(now, *reset), inDays(now, *reset)))
		} else {
			n.body = append(n.body, "没有设重置日，不会自己恢复。")
		}
		n.body = append(n.body, "想现在恢复："+where+"，调大限额或清零用量。")
		return n
	}, back("🟢 "+sh.Name+" 的线路恢复了"))

	r.track(ctx, key("expiring", "user", sh.ID), live && sh.ExpiresAt != nil && sh.ExpiresAt.Sub(now) <= 3*24*time.Hour, func() notice {
		return notice{title: fmt.Sprintf("⏳ %s 快到期了", sh.Name), body: []string{
			fmt.Sprintf("%s 到期，%s。到期后这个用户的线路自动停。", at(now, *sh.ExpiresAt), inDays(now, *sh.ExpiresAt)),
			"续期：" + where + "，改到期时间。",
		}}
	}, nil)

	r.track(ctx, key("expired", "user", sh.ID), sh.Status == domain.ShareExpired, func() notice {
		n := notice{title: "⛔ " + sh.Name + " 到期了，线路已停"}
		if sh.ExpiresAt != nil {
			n.body = append(n.body, "到期时间 "+at(now, *sh.ExpiresAt)+"。")
		}
		n.body = append(n.body, "续期："+where+"，改到期时间。")
		return n
	}, back("🟢 "+sh.Name+" 续上了，线路恢复"))
}
