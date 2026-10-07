package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"ctlvps/internal/domain"
)

const lineCols = `id, name, entry_node_id, landing_node_id, sort_order, enabled, created_at, updated_at`

func scanLine(sc interface{ Scan(...any) error }) (domain.Line, error) {
	var v domain.Line
	var landing sql.NullInt64
	var enabled int
	var created, updated string
	if err := sc.Scan(&v.ID, &v.Name, &v.EntryNodeID, &landing, &v.SortOrder, &enabled, &created, &updated); err != nil {
		return v, err
	}
	v.LandingNodeID = intPtr(landing)
	v.Enabled = enabled == 1
	v.CreatedAt = parseTime(created)
	v.UpdatedAt = parseTime(updated)
	return v, nil
}

// ListLines returns every line in menu order.
func (s *Store) ListLines(ctx context.Context) ([]domain.Line, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+lineCols+` FROM lines ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Line{}
	for rows.Next() {
		v, err := scanLine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetLine fetches one line.
func (s *Store) GetLine(ctx context.Context, id int64) (domain.Line, error) {
	v, err := scanLine(s.db.QueryRowContext(ctx, `SELECT `+lineCols+` FROM lines WHERE id=?`, id))
	if isNoRows(err) {
		return v, ErrNotFound
	}
	return v, err
}

// checkLine accepts only listeners several users can share: deployed nodes
// of a multi-user protocol that own a port and keep the default egress.
func (s *Store) checkLine(ctx context.Context, v *domain.Line) error {
	v.Name = strings.TrimSpace(v.Name)
	if v.Name == "" {
		return errors.New("线路名称不能为空")
	}
	if v.LandingNodeID != nil && *v.LandingNodeID == v.EntryNodeID {
		return errors.New("入口和落地不能是同一个入站")
	}
	ids := []int64{v.EntryNodeID}
	if v.LandingNodeID != nil {
		ids = append(ids, *v.LandingNodeID)
	}
	for i, id := range ids {
		role := "入口"
		if i == 1 {
			role = "落地"
		}
		n, err := s.GetNode(ctx, id)
		if err != nil {
			return errors.New(role + "入站不存在")
		}
		if err := LineNodeUsable(n); err != nil {
			return errors.New(role + "入站" + err.Error())
		}
		if i == 1 && !domain.ProtocolLanding(n.Protocol) {
			return errors.New("落地入站只能是 VLESS Reality 或 Shadowsocks 2022：入口机要能不靠证书确认落地机的身份")
		}
	}
	return nil
}

// LineNodeUsable reports whether members can be attached to n.
func LineNodeUsable(n domain.Node) error {
	switch {
	case n.Source != domain.NodeDeployed || n.ServerID == nil || n.AttachNodeID != nil || n.ShareID != nil:
		return errors.New("必须是在服务器上新建的入站")
	case n.Revoked:
		return errors.New("已撤销")
	case !domain.ProtocolShareable(n.Protocol):
		return errors.New("用的协议一个端口只有一个身份（Snell、mieru、WireGuard），不能多人共用")
	case n.Network != nil:
		return errors.New("设置了自定义监听或出口，暂不支持多人共用")
	}
	return nil
}

// CreateLine inserts a line after validating its nodes.
func (s *Store) CreateLine(ctx context.Context, v *domain.Line) error {
	if err := s.checkLine(ctx, v); err != nil {
		return err
	}
	now := s.Now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO lines(name,entry_node_id,landing_node_id,sort_order,enabled,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`,
		v.Name, v.EntryNodeID, nullInt(v.LandingNodeID), v.SortOrder, b2i(v.Enabled), fmtTime(now), fmtTime(now))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return errors.New("已有同名线路")
		}
		return err
	}
	v.ID, _ = res.LastInsertId()
	v.CreatedAt, v.UpdatedAt = now, now
	return nil
}

// UpdateLine saves all editable fields.
func (s *Store) UpdateLine(ctx context.Context, v *domain.Line) error {
	if err := s.checkLine(ctx, v); err != nil {
		return err
	}
	now := s.Now()
	res, err := s.db.ExecContext(ctx, `UPDATE lines SET name=?,entry_node_id=?,landing_node_id=?,sort_order=?,enabled=?,updated_at=? WHERE id=?`,
		v.Name, v.EntryNodeID, nullInt(v.LandingNodeID), v.SortOrder, b2i(v.Enabled), fmtTime(now), v.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return errors.New("已有同名线路")
		}
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	v.UpdatedAt = now
	return nil
}

// DeleteLine removes a line. Shares keep their stale id, which simply no
// longer resolves.
func (s *Store) DeleteLine(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM lines WHERE id=?`, id)
	return err
}

// ShareLines resolves the enabled lines a share may use, in menu order.
func (s *Store) ShareLines(ctx context.Context, sh domain.Share) ([]domain.Line, error) {
	if sh.LineMode == domain.ShareLinesNone {
		return nil, nil
	}
	all, err := s.ListLines(ctx)
	if err != nil {
		return nil, err
	}
	want := map[int64]bool{}
	for _, id := range sh.LineIDs {
		want[id] = true
	}
	out := []domain.Line{}
	for _, l := range all {
		if l.Enabled && (sh.LineMode == domain.ShareLinesAll || want[l.ID]) {
			out = append(out, l)
		}
	}
	return out, nil
}
