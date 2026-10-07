package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"ctlvps/internal/domain"
)

// Location is the timezone whose midnight starts a reset day. UTC until the
// panel is told otherwise.
func (s *Store) Location() *time.Location {
	if loc := s.loc.Load(); loc != nil {
		return loc
	}
	return time.UTC
}

func (s *Store) loadLocation(ctx context.Context) {
	loc := time.UTC
	if name := s.GetSetting(ctx, domain.SettingQuotaTimezone, ""); name != "" {
		if l, err := time.LoadLocation(name); err == nil {
			loc = l
		}
	}
	s.loc.Store(loc)
}

// adoptQuotaAction retires the panel-wide "stop servers that used up their
// quota": where it was on, every server with a quota now carries the choice
// itself.
func (s *Store) adoptQuotaAction(ctx context.Context) error {
	const key = "quota.action"
	switch s.GetSetting(ctx, key, "") {
	case "":
		return nil
	case "disable", "stop":
		if _, err := s.db.ExecContext(ctx, `UPDATE servers SET quota_stop=1 WHERE quota_bytes>0`); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, key)
	return err
}

// ServerPeriod is what a server's NIC carried in one billing period, plus the
// correction that brings the billed amount in line with the host's own count.
type ServerPeriod struct {
	Start  time.Time
	Rx, Tx int64
	Adjust int64
}

// ServerPeriod returns the running period of a server, ErrNotFound before its
// first heartbeat in reset-day mode.
func (s *Store) ServerPeriod(ctx context.Context, serverID int64) (ServerPeriod, error) {
	return serverPeriod(ctx, s.db, serverID)
}

func serverPeriod(ctx context.Context, q querier, serverID int64) (ServerPeriod, error) {
	var p ServerPeriod
	var start string
	err := q.QueryRowContext(ctx, `SELECT period_start, rx, tx, adjust FROM server_usage WHERE server_id=?`, serverID).Scan(&start, &p.Rx, &p.Tx, &p.Adjust)
	if isNoRows(err) {
		return p, ErrNotFound
	}
	p.Start = parseTime(start)
	return p, err
}

// EnsureServerPeriod makes the running period of a server the one that began
// at start. A server seen for the first time is seeded from its daily history;
// a period that has ended starts again from zero, correction included.
func EnsureServerPeriod(ctx context.Context, tx *sql.Tx, serverID int64, start time.Time) (ServerPeriod, error) {
	p, err := serverPeriod(ctx, tx, serverID)
	switch {
	case errors.Is(err, ErrNotFound):
		p = ServerPeriod{Start: start}
		// Daily buckets are UTC days: the one the period starts in counts whole.
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(up),0), COALESCE(SUM(down),0) FROM traffic_daily WHERE subject=? AND subject_id=? AND bucket>=?`,
			SubjectServer, serverID, start.UTC().Format("2006-01-02")).Scan(&p.Rx, &p.Tx); err != nil {
			return p, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO server_usage(server_id, period_start, rx, tx) VALUES (?,?,?,?)`, serverID, fmtTime(start), p.Rx, p.Tx)
		return p, err
	case err != nil:
		return p, err
	case start.After(p.Start):
		p = ServerPeriod{Start: start}
		_, err = tx.ExecContext(ctx, `UPDATE server_usage SET period_start=?, rx=0, tx=0, adjust=0 WHERE server_id=?`, fmtTime(start), serverID)
		return p, err
	}
	return p, nil
}

// AddServerPeriod counts NIC traffic into the running period.
func AddServerPeriod(ctx context.Context, tx *sql.Tx, serverID, rx, txBytes int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE server_usage SET rx=rx+?, tx=tx+? WHERE server_id=?`, rx, txBytes, serverID)
	return err
}

// SetServerAdjust stores the correction of the running period.
func SetServerAdjust(ctx context.Context, tx *sql.Tx, serverID, adjust int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE server_usage SET adjust=? WHERE server_id=?`, adjust, serverID)
	return err
}

// ClearServerAdjust drops the correction, e.g. when the billing mode changes
// and the corrected figure no longer means the same thing.
func (s *Store) ClearServerAdjust(ctx context.Context, serverID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE server_usage SET adjust=0 WHERE server_id=?`, serverID)
	return err
}

// DropServerPeriod forgets the running period; the next read or heartbeat
// seeds it again. Used when the reset day changes.
func (s *Store) DropServerPeriod(ctx context.Context, serverID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM server_usage WHERE server_id=?`, serverID)
	return err
}

// RebasePeriods moves the stored start of every running period, of users and
// servers alike, to where rebase puts it, without touching the usage.
func (s *Store) RebasePeriods(ctx context.Context, rebase func(resetDay int, stored time.Time) (time.Time, bool)) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		for _, t := range []struct{ list, update string }{
			{`SELECT id, reset_day, period_start FROM shares WHERE reset_day>0`, `UPDATE shares SET period_start=? WHERE id=?`},
			{`SELECT u.server_id, s.quota_reset_day, u.period_start FROM server_usage u JOIN servers s ON s.id=u.server_id WHERE s.quota_reset_day>0`, `UPDATE server_usage SET period_start=? WHERE server_id=?`},
		} {
			rows, err := tx.QueryContext(ctx, t.list)
			if err != nil {
				return err
			}
			moved := map[int64]time.Time{}
			for rows.Next() {
				var id int64
				var day int
				var stored string
				if err := rows.Scan(&id, &day, &stored); err != nil {
					rows.Close()
					return err
				}
				if start, ok := rebase(day, parseTime(stored)); ok {
					moved[id] = start
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for id, start := range moved {
				if _, err := tx.ExecContext(ctx, t.update, fmtTime(start), id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
