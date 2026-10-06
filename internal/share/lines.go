package share

import (
	"bytes"
	"context"

	"ctlvps/internal/domain"
	"ctlvps/internal/provision"
	"ctlvps/internal/store"
)

// memberKey identifies one credential of a share. A node used both as an
// entry and as a landing gets two: only the entry role is charged, and one
// credential cannot be charged for some connections and free for others.
type memberKey struct {
	parent  int64
	landing bool
}

// ensureMembers gives the share its own credential on every entry and landing
// of its lines and revokes credentials whose line is gone. Servers whose
// desired state may have changed are added to affected.
func (m *Manager) ensureMembers(ctx context.Context, sh *domain.Share, affected map[int64]bool) error {
	existing, err := m.Store.ListNodes(ctx, store.NodeFilter{ShareID: &sh.ID, IncludeRevoked: true})
	if err != nil {
		return err
	}
	have := map[memberKey]domain.Node{}
	for _, n := range existing {
		if n.AttachNodeID != nil {
			have[memberKey{*n.AttachNodeID, n.Uncounted}] = n
		}
	}
	lines, err := m.Store.ShareLines(ctx, *sh)
	if err != nil {
		return err
	}
	if sh.Status == domain.ShareRevoked {
		lines = nil
	}
	want := map[memberKey]bool{}
	var order []memberKey
	add := func(k memberKey) {
		if !want[k] {
			want[k] = true
			order = append(order, k)
		}
	}
	for _, l := range lines {
		add(memberKey{l.EntryNodeID, false})
		if l.LandingNodeID != nil {
			add(memberKey{*l.LandingNodeID, true})
		}
	}
	for _, k := range order {
		parent, err := m.Store.GetNode(ctx, k.parent)
		if err != nil || store.LineNodeUsable(parent) != nil {
			delete(want, k) // the line cannot work; drop a stale credential below
			continue
		}
		name := sh.Name + " · " + parent.Name
		// A landing's allowed sources follow the share's lines, which can
		// change without touching the credential itself.
		affected[*parent.ServerID] = true
		if n, ok := have[k]; ok {
			before := n
			if n.Revoked {
				n.Revoked, n.Enabled = false, true
			}
			if err := provision.SyncMember(&n, parent, before.Revoked); err != nil {
				return err
			}
			n.Name = name
			if sh.UserID != nil {
				n.OwnerUserID = *sh.UserID
			}
			if n.Revoked != before.Revoked || n.Name != before.Name || n.OwnerUserID != before.OwnerUserID || n.Server != before.Server || n.Port != before.Port ||
				!bytes.Equal(n.Params, before.Params) || !bytes.Equal(n.ServerParams, before.ServerParams) {
				if err := m.Store.UpdateNode(ctx, &n); err != nil {
					return err
				}
			}
			continue
		}
		node, err := provision.NewMember(parent, name)
		if err != nil {
			return err
		}
		sid := sh.ID
		node.ShareID, node.Uncounted = &sid, k.landing
		if sh.UserID != nil {
			node.OwnerUserID = *sh.UserID
		}
		if err := m.Store.CreateNode(ctx, &node); err != nil {
			return err
		}
	}
	for k, n := range have {
		if want[k] || n.Revoked {
			continue
		}
		n.Revoked, n.Enabled = true, false
		if err := m.Store.UpdateNode(ctx, &n); err != nil {
			return err
		}
		if n.ServerID != nil {
			affected[*n.ServerID] = true
		}
	}
	return nil
}

// SyncLines reconciles every share that follows lines. Call it after lines or
// the nodes they use change.
func (m *Manager) SyncLines(ctx context.Context) error {
	shares, err := m.Store.ListShares(ctx, nil)
	if err != nil {
		return err
	}
	for i := range shares {
		if shares[i].LineMode == domain.ShareLinesNone {
			continue
		}
		if err := m.EnsureNodes(ctx, &shares[i]); err != nil {
			return err
		}
	}
	return nil
}
