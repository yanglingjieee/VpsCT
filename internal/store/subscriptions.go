package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"ctlvps/internal/auth"
	"ctlvps/internal/domain"
)

// The old uploaded_content column is deliberately left untouched in SQLite so
// retiring configuration hosting does not delete the operator's original data.
const subCols = `id, name, kind, token, token_hash, token_hint, short_code, template_id, default_format, proxy_groups, chains, rules, rule_providers, node_selection, source_external_id, expire_at, traffic_limit_bytes, reset_day, userinfo_header, show_info_nodes, owner_user_id, allowed_user_ids, share_id, enabled, access_count, last_access_at, created_at, updated_at`

func (s *Store) scanSub(sc interface{ Scan(...any) error }) (domain.Subscription, error) {
	var v domain.Subscription
	var templateID, srcExt, shareID sql.NullInt64
	var expire, lastAccess sql.NullString
	var groups, chains, rules, providers, sel, allowed, created, updated string
	var userinfo, showInfo, enabled int
	if err := sc.Scan(&v.ID, &v.Name, &v.Kind, s.scanSecret("subscriptions.token", &v.Token), &v.TokenHash, &v.TokenHint, s.scanSecret("subscriptions.short_code", &v.ShortCode), &templateID, &v.DefaultFormat, &groups, &chains, &rules, &providers, &sel,
		&srcExt, &expire, &v.TrafficLimitBytes, &v.ResetDay, &userinfo, &showInfo, &v.OwnerUserID, &allowed, &shareID, &enabled, &v.AccessCount, &lastAccess, &created, &updated); err != nil {
		return v, err
	}
	v.TemplateID = intPtr(templateID)
	v.SourceExternalID = intPtr(srcExt)
	v.ShareID = intPtr(shareID)
	v.ExpireAt = parseTimePtr(expire)
	v.LastAccessAt = parseTimePtr(lastAccess)
	v.ProxyGroups = jsonList[domain.ProxyGroup](groups)
	v.Chains = jsonList[domain.ChainSpec](chains)
	v.Rules = jsonList[string](rules)
	v.RuleProviders = rawOrEmpty(providers)
	_ = json.Unmarshal([]byte(sel), &v.NodeSelection)
	if v.NodeSelection.NodeIDs == nil {
		v.NodeSelection.NodeIDs = []int64{}
	}
	if v.NodeSelection.ExternalSubIDs == nil {
		v.NodeSelection.ExternalSubIDs = []int64{}
	}
	v.AllowedUserIDs = jsonList[int64](allowed)
	v.UserinfoHeader = userinfo == 1
	v.ShowInfoNodes = showInfo == 1
	v.Enabled = enabled == 1
	v.CreatedAt = parseTime(created)
	v.UpdatedAt = parseTime(updated)
	return v, nil
}

func (s *Store) subArgs(v *domain.Subscription) []any {
	if v.ProxyGroups == nil {
		v.ProxyGroups = []domain.ProxyGroup{}
	}
	if v.Chains == nil {
		v.Chains = []domain.ChainSpec{}
	}
	if v.Rules == nil {
		v.Rules = []string{}
	}
	if len(v.RuleProviders) == 0 {
		v.RuleProviders = []byte("{}")
	}
	if v.AllowedUserIDs == nil {
		v.AllowedUserIDs = []int64{}
	}
	if v.DefaultFormat == "" {
		v.DefaultFormat = "mihomo"
	}
	return []any{v.Name, v.Kind, s.seal("subscriptions.token", v.Token), v.TokenHash, v.TokenHint, s.seal("subscriptions.short_code", v.ShortCode), auth.HashToken(v.ShortCode), nullInt(v.TemplateID), v.DefaultFormat, jsonStr(v.ProxyGroups), jsonStr(v.Chains), jsonStr(v.Rules), string(v.RuleProviders), jsonStr(v.NodeSelection),
		nullInt(v.SourceExternalID), fmtTimePtr(v.ExpireAt), v.TrafficLimitBytes, v.ResetDay, b2i(v.UserinfoHeader), b2i(v.ShowInfoNodes), v.OwnerUserID, jsonStr(v.AllowedUserIDs), nullInt(v.ShareID), b2i(v.Enabled)}
}

// CreateSubscription inserts a subscription.
func (s *Store) CreateSubscription(ctx context.Context, v *domain.Subscription) error {
	now := s.Now()
	args := append(s.subArgs(v), fmtTime(now), fmtTime(now))
	res, err := s.db.ExecContext(ctx, `INSERT INTO subscriptions(name,kind,token,token_hash,token_hint,short_code,short_code_hash,template_id,default_format,proxy_groups,chains,rules,rule_providers,node_selection,source_external_id,expire_at,traffic_limit_bytes,reset_day,userinfo_header,show_info_nodes,owner_user_id,allowed_user_ids,share_id,enabled,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...)
	if err != nil {
		return err
	}
	v.ID, _ = res.LastInsertId()
	v.CreatedAt, v.UpdatedAt = now, now
	return nil
}

// UpdateSubscription saves all editable fields.
func (s *Store) UpdateSubscription(ctx context.Context, v *domain.Subscription) error {
	now := s.Now()
	args := append(s.subArgs(v), fmtTime(now), v.ID)
	_, err := s.db.ExecContext(ctx, `UPDATE subscriptions SET name=?,kind=?,token=?,token_hash=?,token_hint=?,short_code=?,short_code_hash=?,template_id=?,default_format=?,proxy_groups=?,chains=?,rules=?,rule_providers=?,node_selection=?,source_external_id=?,expire_at=?,traffic_limit_bytes=?,reset_day=?,userinfo_header=?,show_info_nodes=?,owner_user_id=?,allowed_user_ids=?,share_id=?,enabled=?,updated_at=? WHERE id=?`, args...)
	v.UpdatedAt = now
	return err
}

// DeleteSubscription removes a subscription.
func (s *Store) DeleteSubscription(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM subscriptions WHERE id=?`, id)
	return err
}

// GetSubscription fetches one.
func (s *Store) GetSubscription(ctx context.Context, id int64) (domain.Subscription, error) {
	v, err := s.scanSub(s.db.QueryRowContext(ctx, `SELECT `+subCols+` FROM subscriptions WHERE id=?`, id))
	if isNoRows(err) {
		return v, ErrNotFound
	}
	return v, err
}

// GetSubscriptionByTokenHash resolves /s/<token>.
func (s *Store) GetSubscriptionByTokenHash(ctx context.Context, hash string) (domain.Subscription, error) {
	v, err := s.scanSub(s.db.QueryRowContext(ctx, `SELECT `+subCols+` FROM subscriptions WHERE token_hash=?`, hash))
	if isNoRows(err) {
		return v, ErrNotFound
	}
	return v, err
}

// GetSubscriptionByShortCode resolves /r/<code>.
func (s *Store) GetSubscriptionByShortCode(ctx context.Context, code string) (domain.Subscription, error) {
	if code == "" {
		return domain.Subscription{}, ErrNotFound
	}
	v, err := s.scanSub(s.db.QueryRowContext(ctx, `SELECT `+subCols+` FROM subscriptions WHERE short_code_hash=?`, auth.HashToken(code)))
	if isNoRows(err) {
		return v, ErrNotFound
	}
	return v, err
}

// GetSubscriptionByShare returns the share's subscription.
func (s *Store) GetSubscriptionByShare(ctx context.Context, shareID int64) (domain.Subscription, error) {
	v, err := s.scanSub(s.db.QueryRowContext(ctx, `SELECT `+subCols+` FROM subscriptions WHERE share_id=? ORDER BY id LIMIT 1`, shareID))
	if isNoRows(err) {
		return v, ErrNotFound
	}
	return v, err
}

// ListSubscriptions returns all subscriptions.
func (s *Store) ListSubscriptions(ctx context.Context) ([]domain.Subscription, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+subCols+` FROM subscriptions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Subscription{}
	for rows.Next() {
		v, err := s.scanSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// TouchSubscription bumps access statistics.
func (s *Store) TouchSubscription(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE subscriptions SET access_count=access_count+1, last_access_at=? WHERE id=?`, fmtTime(s.Now()), id)
	return err
}
