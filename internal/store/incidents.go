package store

import (
	"context"
	"database/sql"
	"time"

	"ctlvps/internal/domain"
)

const incidentCols = `id, key, title, opened_at, resolved_at, message_id`

func scanIncidents(rows *sql.Rows) ([]domain.Incident, error) {
	defer rows.Close()
	out := []domain.Incident{}
	for rows.Next() {
		var v domain.Incident
		var opened string
		var resolved sql.NullString
		if err := rows.Scan(&v.ID, &v.Key, &v.Title, &opened, &resolved, &v.MessageID); err != nil {
			return nil, err
		}
		v.OpenedAt = parseTime(opened)
		v.ResolvedAt = parseTimePtr(resolved)
		out = append(out, v)
	}
	return out, rows.Err()
}

// OpenIncidents returns what has not cleared yet, oldest first.
func (s *Store) OpenIncidents(ctx context.Context) ([]domain.Incident, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+incidentCols+` FROM incidents WHERE resolved_at IS NULL ORDER BY opened_at, id`)
	if err != nil {
		return nil, err
	}
	return scanIncidents(rows)
}

// OpenIncident records that v.Key started. An event that is over as soon as
// it is told comes with ResolvedAt already set.
func (s *Store) OpenIncident(ctx context.Context, v *domain.Incident) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO incidents(key, title, opened_at, resolved_at, message_id) VALUES (?,?,?,?,?)`,
		v.Key, v.Title, fmtTime(v.OpenedAt), fmtTimePtr(v.ResolvedAt), v.MessageID)
	if err != nil {
		return err
	}
	v.ID, err = res.LastInsertId()
	return err
}

// SetIncidentMessage remembers the message that announced an incident.
func (s *Store) SetIncidentMessage(ctx context.Context, id, messageID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE incidents SET message_id=? WHERE id=?`, messageID, id)
	return err
}

// ResolveIncident records that an incident cleared.
func (s *Store) ResolveIncident(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE incidents SET resolved_at=? WHERE id=? AND resolved_at IS NULL`, fmtTime(at), id)
	return err
}

// IncidentsBetween returns what started in [from, to), oldest first.
func (s *Store) IncidentsBetween(ctx context.Context, from, to time.Time) ([]domain.Incident, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+incidentCols+` FROM incidents WHERE opened_at>=? AND opened_at<? ORDER BY opened_at, id`, fmtTime(from), fmtTime(to))
	if err != nil {
		return nil, err
	}
	return scanIncidents(rows)
}

// PruneIncidents forgets what cleared longer than keep ago.
func (s *Store) PruneIncidents(ctx context.Context, keep time.Duration) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM incidents WHERE resolved_at IS NOT NULL AND resolved_at<?`, fmtTime(s.Now().Add(-keep)))
	return err
}
