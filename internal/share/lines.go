package share

import (
	"bytes"
	"context"

	"ctlvps/internal/domain"
	"ctlvps/internal/provision"
	"ctlvps/internal/store"
)

// memberKey identifies one credential of a share: every line has its own on
// the entry, and a relay line a second one on its landing. Usage is therefore
// known per user, per line and per machine.
type memberKey struct {
	line    int64
	landing bool
}

// ensureMembers gives the share its credentials for the lines it follows and
// revokes those whose line is gone. Servers whose desired state may have
// changed are added to affected.
func (m *Manager) ensureMembers(ctx context.Context, sh *domain.Share, affected map[int64]bool) error {
	existing, err := m.Store.ListNodes(ctx, store.NodeFilter{ShareID: &sh.ID, IncludeRevoked: true})
	if err != nil {
		return err
	}
	have := map[memberKey]domain.Node{}
	for _, n := range existing {
		if n.AttachNodeID != nil && n.LineID != nil {
			have[memberKey{*n.LineID, n.Landing}] = n
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
	ensure := func(l domain.Line, parent domain.Node, landing bool) error {
		k := memberKey{l.ID, landing}
		want[k] = true
		// A relay member's target and a landing's allowed source live on
		// another server and can change without touching this credential.
		affected[*parent.ServerID] = true
		name := sh.Name + " · " + l.Name
		if landing {
			name += " · 落地"
		}
		n, ok := have[k]
		if ok && *n.AttachNodeID != parent.ID {
			// The line now runs through another listener: a new identity
			// there, the old one settles and disappears.
			if n.ServerID != nil {
				affected[*n.ServerID] = true
			}
			if err := m.Store.DeleteNode(ctx, n.ID); err != nil {
				return err
			}
			ok = false
		}
		if !ok {
			node, err := provision.NewMember(parent, name)
			if err != nil {
				return err
			}
			sid, lid := sh.ID, l.ID
			node.ShareID, node.LineID, node.Landing = &sid, &lid, landing
			if sh.UserID != nil {
				node.OwnerUserID = *sh.UserID
			}
			return m.Store.CreateNode(ctx, &node)
		}
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
		if n.Revoked == before.Revoked && n.Name == before.Name && n.OwnerUserID == before.OwnerUserID && n.Server == before.Server && n.Port == before.Port &&
			bytes.Equal(n.Params, before.Params) && bytes.Equal(n.ServerParams, before.ServerParams) {
			return nil
		}
		return m.Store.UpdateNode(ctx, &n)
	}
	usable := func(id int64) (domain.Node, bool) {
		n, err := m.Store.GetNode(ctx, id)
		return n, err == nil && store.LineNodeUsable(n) == nil
	}
	for _, l := range lines {
		entry, ok := usable(l.EntryNodeID)
		if !ok {
			continue
		}
		if l.LandingNodeID == nil {
			if err := ensure(l, entry, false); err != nil {
				return err
			}
			continue
		}
		// A relay line needs both ends. Half of one would be a different
		// line: the user would leave from the entry server instead.
		landing, ok := usable(*l.LandingNodeID)
		if !ok {
			continue
		}
		if err := ensure(l, landing, true); err != nil {
			return err
		}
		if err := ensure(l, entry, false); err != nil {
			return err
		}
	}
	for k, n := range have {
		if want[k] || n.Revoked {
			continue
		}
		if cur, err := m.Store.GetNode(ctx, n.ID); err != nil || *cur.AttachNodeID != *n.AttachNodeID {
			continue // replaced above
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
	// Credentials deleted together with their line or listener leave no row
	// to tell which server lost them. Publishing is a no-op when unchanged.
	return m.Desired.PublishAll(ctx)
}
