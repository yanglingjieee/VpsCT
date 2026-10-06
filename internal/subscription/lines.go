package subscription

import (
	"context"

	"ctlvps/internal/domain"
	"ctlvps/internal/provision"
)

// appendLines renders a share's lines with the share's own credentials. Every
// line is one ordinary proxy on its entry server: relaying to a landing is
// done by the entry, so any client can use any line.
func (s *Service) appendLines(ctx context.Context, b *Bundle, hosts map[int64]string, sh domain.Share, members []domain.Node) error {
	lines, err := s.Store.ShareLines(ctx, sh)
	if err != nil || len(lines) == 0 {
		return err
	}
	own := map[int64]domain.Node{}
	for _, m := range members {
		if m.LineID != nil && !m.Landing {
			own[*m.LineID] = m
		}
	}
	for _, l := range lines {
		m, ok := own[l.ID]
		if !ok {
			continue
		}
		entry, err := s.Store.GetNode(ctx, l.EntryNodeID)
		if err != nil || !entry.Enabled || entry.Revoked || m.AttachNodeID == nil || *m.AttachNodeID != entry.ID {
			continue
		}
		// The listener is authoritative for everything but the secret.
		if err := provision.SyncMember(&m, entry, false); err != nil {
			continue
		}
		n := entry
		n.Params, n.Name = m.Params, l.Name
		b.Proxies = append(b.Proxies, ProxyFor(n, hosts))
		b.Order = append(b.Order, l.Name)
	}
	return nil
}
