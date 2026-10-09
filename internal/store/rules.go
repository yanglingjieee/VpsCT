package store

import (
	"context"
	"database/sql"

	"ctlvps/internal/domain"
	"ctlvps/internal/ruleset"
)

// Rules returns the panel's rules; the zero value while none were saved.
func (s *Store) Rules(ctx context.Context) (domain.Rules, error) {
	var v domain.Rules
	var updated string
	err := s.db.QueryRowContext(ctx, `SELECT rules, group_name, updated_at FROM rulesets ORDER BY id LIMIT 1`).Scan(&v.Rules, &v.GroupName, &updated)
	if isNoRows(err) {
		return v, nil
	}
	v.UpdatedAt = parseTime(updated)
	return v, err
}

// SaveRules replaces the panel's rules. The table holds this one row.
func (s *Store) SaveRules(ctx context.Context, v *domain.Rules) error {
	now := s.Now()
	res, err := s.db.ExecContext(ctx, `UPDATE rulesets SET rules=?, group_name=?, updated_at=?`, v.Rules, v.GroupName, fmtTime(now))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO rulesets(name,rules,group_name,created_at,updated_at) VALUES ('rules',?,?,?,?)`, v.Rules, v.GroupName, fmtTime(now), fmtTime(now)); err != nil {
			return err
		}
	}
	v.UpdatedAt = now
	return nil
}

const rulesetUnifyMigration = `ALTER TABLE rulesets ADD COLUMN rules TEXT NOT NULL DEFAULT '';
ALTER TABLE rulesets ADD COLUMN group_name TEXT NOT NULL DEFAULT '';`

// unifyRulesets gives every rule set written as one profile per client its
// single list of rules, read from the Clash profile. A rule set that had
// none keeps an empty list: everything takes the chosen line.
func unifyRulesets(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, mihomo FROM rulesets`)
	if err != nil {
		return err
	}
	profiles := map[int64]string{}
	for rows.Next() {
		var id int64
		var profile string
		if err := rows.Scan(&id, &profile); err != nil {
			rows.Close()
			return err
		}
		profiles[id] = profile
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for id, profile := range profiles {
		rules, group := ruleset.FromClashProfile(profile)
		if _, err := tx.ExecContext(ctx, `UPDATE rulesets SET rules=?, group_name=? WHERE id=?`, rules, group, id); err != nil {
			return err
		}
	}
	return nil
}
