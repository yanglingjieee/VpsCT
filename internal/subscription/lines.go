package subscription

import (
	"context"

	"ctlvps/internal/domain"
	"ctlvps/internal/provision"
	"ctlvps/internal/proxynode"
)

// appendLines renders a share's lines with the share's own credentials: a
// direct line is its entry, a relay line is its landing dialed through the
// entry. Names and order are the lines'.
func (s *Service) appendLines(ctx context.Context, b *Bundle, hosts map[int64]string, sh domain.Share, members []domain.Node) error {
	lines, err := s.Store.ShareLines(ctx, sh)
	if err != nil || len(lines) == 0 {
		return err
	}
	type key struct {
		parent  int64
		landing bool
	}
	own := map[key]domain.Node{}
	for _, m := range members {
		own[key{*m.AttachNodeID, m.Uncounted}] = m
	}
	parents := map[int64]*domain.Node{}
	parent := func(id int64) *domain.Node {
		if p, ok := parents[id]; ok {
			return p
		}
		parents[id] = nil
		if n, err := s.Store.GetNode(ctx, id); err == nil && n.Enabled && !n.Revoked {
			parents[id] = &n
		}
		return parents[id]
	}
	proxy := func(id int64, landing bool, name string) (proxynode.Proxy, bool) {
		m, ok := own[key{id, landing}]
		p := parent(id)
		if !ok || p == nil {
			return proxynode.Proxy{}, false
		}
		// The listener is authoritative for everything but the secret.
		if err := provision.SyncMember(&m, *p, false); err != nil {
			return proxynode.Proxy{}, false
		}
		n := *p
		n.Params, n.Name = m.Params, name
		return ProxyFor(n, hosts), true
	}
	used := map[string]bool{}
	for _, l := range lines {
		used[l.Name] = true
	}
	// An entry is named after its direct line when it has one.
	entryName := map[int64]string{}
	for _, l := range lines {
		if _, ok := entryName[l.EntryNodeID]; l.LandingNodeID == nil && !ok {
			entryName[l.EntryNodeID] = l.Name
		}
	}
	added := map[int64]bool{}
	entry := func(id int64) (string, bool) {
		name, direct := entryName[id]
		if added[id] {
			return name, true
		}
		if !direct {
			p := parent(id)
			if p == nil {
				return "", false
			}
			name = p.Name
			for used[name] {
				name += " · 入口"
			}
		}
		px, ok := proxy(id, false, name)
		if !ok {
			return "", false
		}
		used[name], added[id], entryName[id] = true, true, name
		b.Proxies = append(b.Proxies, px)
		return name, true
	}
	for _, l := range lines {
		via, ok := entry(l.EntryNodeID)
		if !ok {
			continue
		}
		if l.LandingNodeID == nil {
			if via == l.Name {
				b.Order = append(b.Order, l.Name)
			}
			continue
		}
		px, ok := proxy(*l.LandingNodeID, true, l.Name)
		if !ok {
			continue
		}
		b.Chains = append(b.Chains, ChainedProxy{Proxy: px, Via: via})
		b.Order = append(b.Order, l.Name)
	}
	return nil
}
