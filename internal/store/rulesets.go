package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"ctlvps/internal/domain"
	"ctlvps/internal/ruleset"
)

const rulesetCols = `id, name, description, rules, group_name, sort_order, created_at, updated_at`

func scanRuleset(sc interface{ Scan(...any) error }) (domain.Ruleset, error) {
	var v domain.Ruleset
	var created, updated string
	if err := sc.Scan(&v.ID, &v.Name, &v.Description, &v.Rules, &v.GroupName, &v.SortOrder, &created, &updated); err != nil {
		return v, err
	}
	v.CreatedAt, v.UpdatedAt = parseTime(created), parseTime(updated)
	return v, nil
}

// ListRulesets returns every rule set in menu order.
func (s *Store) ListRulesets(ctx context.Context) ([]domain.Ruleset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+rulesetCols+` FROM rulesets ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Ruleset{}
	for rows.Next() {
		v, err := scanRuleset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetRuleset fetches one rule set.
func (s *Store) GetRuleset(ctx context.Context, id int64) (domain.Ruleset, error) {
	v, err := scanRuleset(s.db.QueryRowContext(ctx, `SELECT `+rulesetCols+` FROM rulesets WHERE id=?`, id))
	if isNoRows(err) {
		return v, ErrNotFound
	}
	return v, err
}

func rulesetErr(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return errors.New("已有同名规则")
	}
	return err
}

// CreateRuleset inserts a rule set.
func (s *Store) CreateRuleset(ctx context.Context, v *domain.Ruleset) error {
	if v.Name = strings.TrimSpace(v.Name); v.Name == "" {
		return errors.New("规则名称不能为空")
	}
	now := s.Now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO rulesets(name,description,rules,group_name,sort_order,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`,
		v.Name, v.Description, v.Rules, v.GroupName, v.SortOrder, fmtTime(now), fmtTime(now))
	if err != nil {
		return rulesetErr(err)
	}
	v.ID, _ = res.LastInsertId()
	v.CreatedAt, v.UpdatedAt = now, now
	return nil
}

// UpdateRuleset saves all editable fields.
func (s *Store) UpdateRuleset(ctx context.Context, v *domain.Ruleset) error {
	if v.Name = strings.TrimSpace(v.Name); v.Name == "" {
		return errors.New("规则名称不能为空")
	}
	now := s.Now()
	res, err := s.db.ExecContext(ctx, `UPDATE rulesets SET name=?,description=?,rules=?,group_name=?,sort_order=?,updated_at=? WHERE id=?`,
		v.Name, v.Description, v.Rules, v.GroupName, v.SortOrder, fmtTime(now), v.ID)
	if err != nil {
		return rulesetErr(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	v.UpdatedAt = now
	return nil
}

// DeleteRuleset removes a rule set; its users fall back to no rules.
func (s *Store) DeleteRuleset(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM rulesets WHERE id=?`, id)
	return err
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
