package subscription

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"ctlvps/internal/domain"
	"ctlvps/internal/proxynode"
	"ctlvps/internal/ruleset"
	"ctlvps/internal/store"
)

// Service builds and renders users' profiles on top of the store, and keeps
// external nodes in step with where they come from.
type Service struct {
	Store   *store.Store
	Fetcher *Fetcher
	Now     func() time.Time
}

// NewService constructs a Service.
func NewService(st *store.Store) *Service {
	return &Service{Store: st, Fetcher: NewFetcher(), Now: func() time.Time { return time.Now().UTC() }}
}

// serverHosts maps server id -> best public host for deployed nodes.
func (s *Service) serverHosts(ctx context.Context) map[int64]string {
	out := map[int64]string{}
	servers, err := s.Store.ListServers(ctx)
	if err != nil {
		return out
	}
	agents, _ := s.Store.ListAgents(ctx)
	byServer := map[int64]domain.Agent{}
	for _, a := range agents {
		byServer[a.ServerID] = a
	}
	for _, sv := range servers {
		host := sv.PublicHost
		if host == "" {
			if a, ok := byServer[sv.ID]; ok {
				if a.PublicIPv4 != "" {
					host = a.PublicIPv4
				} else {
					host = a.PublicIPv6
				}
			}
		}
		out[sv.ID] = host
	}
	return out
}

// ProxyFor converts a node into a client proxy, filling the server address of
// deployed nodes from the VPS record.
func ProxyFor(n domain.Node, hosts map[int64]string) proxynode.Proxy {
	p := proxynode.FromDomain(n)
	inherits := n.Network == nil || n.Network.AdvertiseMode == "inherit"
	if n.Network != nil && inherits && n.ServerID != nil && hosts[*n.ServerID] != "" {
		p.Server = hosts[*n.ServerID]
	}
	if p.Server == "" && n.ServerID != nil && inherits {
		p.Server = hosts[*n.ServerID]
	}
	if p.Port == 0 && n.ListenPort > 0 {
		p.Port = n.ListenPort
	}
	return p
}

// Build collects what a user's profile is made of: their lines, in menu
// order, then any external nodes they were given. A user who is paused,
// out of traffic or expired has none, and a line saying why.
func (s *Service) Build(ctx context.Context, sub domain.Subscription) (*Bundle, error) {
	if sub.Kind != domain.SubShare || sub.ShareID == nil {
		return nil, errors.New("这个链接不属于任何用户")
	}
	now := s.Now()
	b := &Bundle{Name: sub.Name, GeneratedAt: now}
	sh, err := s.Store.GetShare(ctx, *sub.ShareID)
	if err != nil {
		return nil, err
	}
	ui := shareUserinfo(sh)
	b.Userinfo = &ui
	if sh.Status != domain.ShareActive {
		b.InfoNodes = []string{shareStatusLine(sh.Status)}
		return b, nil
	}
	owned, err := s.Store.ListNodes(ctx, store.NodeFilter{ShareID: sub.ShareID, OnlyEnabled: true})
	if err != nil {
		return nil, err
	}
	hosts := s.serverHosts(ctx)
	// Member credentials are not proxies by themselves: they appear as the
	// lines they belong to.
	var members, extra []domain.Node
	for _, n := range owned {
		if n.AttachNodeID != nil {
			members = append(members, n)
		} else {
			extra = append(extra, n)
		}
	}
	if err := s.appendLines(ctx, b, hosts, sh, members); err != nil {
		return nil, err
	}
	if len(sh.ExtraNodeIDs) > 0 {
		if more, err := s.Store.ListNodes(ctx, store.NodeFilter{IDs: sh.ExtraNodeIDs, OnlyEnabled: true}); err == nil {
			extra = append(extra, more...)
		}
	}
	// Lines keep their names and come first; anything else follows under a
	// name of its own.
	var others []proxynode.Proxy
	for _, n := range extra {
		if n.Source == domain.NodeChain || n.Source == domain.NodeImported && proxynode.IsSubscriptionInfo(proxynode.FromDomain(n)) {
			continue
		}
		others = append(others, ProxyFor(n, hosts))
	}
	taken := map[string]bool{}
	for _, p := range b.Proxies {
		taken[p.Name] = true
	}
	for _, p := range proxynode.DedupeNames(others) {
		if !taken[p.Name] {
			taken[p.Name] = true
			b.Proxies = append(b.Proxies, p)
			b.Order = append(b.Order, p.Name)
		}
	}
	if sub.ShowInfoNodes {
		b.InfoNodes = b.Userinfo.InfoLines(now)
	}
	return b, nil
}

func shareStatusLine(st domain.ShareStatus) string {
	switch st {
	case domain.SharePaused:
		return "已暂停，请联系管理员"
	case domain.ShareExhausted:
		return "本周期流量已用尽"
	case domain.ShareExpired:
		return "已到期"
	case domain.ShareRevoked:
		return "已被撤销"
	}
	return "不可用"
}

// Render builds a user's profile in the given format ("" is Clash). site is
// the panel's public address: a profile fetches the lists its rules name
// there.
func (s *Service) Render(ctx context.Context, sub domain.Subscription, format, site string) (*Rendered, *Bundle, error) {
	b, err := s.Build(ctx, sub)
	if err != nil {
		return nil, nil, err
	}
	if len(b.Proxies) > 10000 {
		return nil, nil, fmt.Errorf("线路过多")
	}
	for _, p := range b.Proxies {
		if err := proxynode.Validate(p); err != nil {
			return nil, nil, err
		}
	}
	var r *Rendered
	switch format = NormalizeFormat(format); format {
	case FormatRaw:
		r, err = RenderRaw(b, false)
	case FormatURIList:
		r, err = RenderRaw(b, true)
	default:
		// Every profile is built from the panel's one list of rules, so one
		// link works in every client and all of them change together.
		rules, group := s.Rules(ctx)
		r, err = RenderProfile(b, format, rules, group, site)
	}
	if err == nil && len(r.Body) > 16<<20 {
		err = fmt.Errorf("生成的配置超过大小限制")
	}
	if err != nil {
		return nil, nil, err
	}
	return r, b, nil
}

// RenderProfile builds the profile of one client family ("shadowrocket", or
// Clash for anything else) from a rule set and fills it with the lines.
func RenderProfile(b *Bundle, kind, rules, group, site string) (*Rendered, error) {
	if kind != FormatShadowrocket {
		kind = FormatMihomo
	}
	profile, err := Profile(kind, rules, group, site)
	if err != nil {
		return nil, err
	}
	if kind == FormatShadowrocket {
		return RenderShadowrocket(b, profile)
	}
	return RenderMihomo(b, profile)
}

// ProfileKinds lists the client families a profile is built for.
var ProfileKinds = []string{FormatMihomo, FormatShadowrocket}

// Rules returns the rules every profile is built from and the name of the
// selector of lines: what was saved in the panel, or the built-in minimum
// (the local network stays local, everything else takes the chosen line)
// while nothing was.
func (s *Service) Rules(ctx context.Context) (rules, group string) {
	rules, group = ruleset.Default, ruleset.DefaultGroup
	if saved, err := s.Store.Rules(ctx); err == nil {
		if strings.TrimSpace(saved.Rules) != "" {
			rules = saved.Rules
		}
		if strings.TrimSpace(saved.GroupName) != "" {
			group = saved.GroupName
		}
	}
	return rules, group
}

// ShareFormats lists the client families to offer a user a profile for; none
// when they receive plain nodes.
func (s *Service) ShareFormats(_ context.Context, sh domain.Share) []string {
	if sh.Delivery == domain.DeliveryNodes {
		return []string{}
	}
	return append([]string{}, ProfileKinds...)
}

// SyncStats summarises one external sync.
type SyncStats struct {
	Added   int      `json:"added"`
	Updated int      `json:"updated"`
	Removed int      `json:"removed"`
	Total   int      `json:"total"`
	Errors  []string `json:"errors,omitempty"`
}

// SyncExternal fetches an external subscription and updates its nodes,
// userinfo and traffic history.
func (s *Service) SyncExternal(ctx context.Context, ext domain.ExternalSubscription) (SyncStats, error) {
	res, err := s.Fetcher.Fetch(ctx, ext.URL, ext.UserAgent)
	if err != nil {
		_ = s.Store.RecordExternalSync(ctx, ext.ID, store.ExternalSyncResult{Err: err.Error()})
		return SyncStats{}, err
	}
	return s.applyExternal(ctx, ext, res)
}

// applyExternal persists a fetched/parsed body.
func (s *Service) applyExternal(ctx context.Context, ext domain.ExternalSubscription, res *FetchResult) (SyncStats, error) {
	// Keep the upstream body for inspection, but never turn status banners into
	// connections. A metadata-only response must not erase working old nodes.
	filtered := make([]proxynode.Proxy, 0, len(res.Proxies))
	for _, p := range res.Proxies {
		if !proxynode.IsSubscriptionInfo(p) {
			filtered = append(filtered, p)
		}
	}
	copyResult := *res
	copyResult.Proxies = filtered
	res = &copyResult
	if len(res.Proxies) == 0 {
		msg := "未解析到任何节点"
		if len(res.ParseErrors) > 0 {
			msg += ": " + strings.Join(res.ParseErrors[:min(3, len(res.ParseErrors))], "; ")
		}
		_ = s.Store.RecordExternalSync(ctx, ext.ID, store.ExternalSyncResult{Err: msg})
		return SyncStats{Errors: res.ParseErrors}, errors.New(msg)
	}
	nodes := make([]domain.Node, 0, len(res.Proxies))
	for _, p := range res.Proxies {
		n := p.ToDomain()
		n.OwnerUserID = ext.OwnerUserID
		nodes = append(nodes, n)
	}
	added, updated, removed, err := s.Store.ReplaceExternalNodes(ctx, ext.ID, nodes)
	if err != nil {
		_ = s.Store.RecordExternalSync(ctx, ext.ID, store.ExternalSyncResult{Err: err.Error()})
		return SyncStats{}, err
	}
	sr := store.ExternalSyncResult{RawContent: res.Body, NodeCount: len(nodes), HasInfo: res.HasUserinfo}
	if res.HasUserinfo {
		sr.Upload, sr.Download, sr.Total, sr.ExpireAt = res.Userinfo.Upload, res.Userinfo.Download, res.Userinfo.Total, res.Userinfo.Expire
		// usage history: delta against last stored totals (upstream resets -> take full)
		dUp := res.Userinfo.Upload - ext.Upload
		dDown := res.Userinfo.Download - ext.Download
		if dUp < 0 {
			dUp = res.Userinfo.Upload
		}
		if dDown < 0 {
			dDown = res.Userinfo.Download
		}
		if ext.LastSyncAt != nil { // skip the very first sync: no baseline
			_ = s.Store.AddTraffic(ctx, store.SubjectExternal, ext.ID, s.Now(), dUp, dDown)
		}
	}
	if err := s.Store.RecordExternalSync(ctx, ext.ID, sr); err != nil {
		return SyncStats{}, err
	}
	return SyncStats{Added: added, Updated: updated, Removed: removed, Total: len(nodes), Errors: res.ParseErrors}, nil
}

// ImportExternalBody applies a manually supplied body (e.g. pasted YAML) to an
// external subscription without fetching.
func (s *Service) ImportExternalBody(ctx context.Context, ext domain.ExternalSubscription, body string) (SyncStats, error) {
	return s.applyExternal(ctx, ext, ParseBody(body))
}

// SyncDue syncs every enabled external subscription whose interval elapsed.
func (s *Service) SyncDue(ctx context.Context, force bool) map[int64]error {
	out := map[int64]error{}
	exts, err := s.Store.ListExternal(ctx)
	if err != nil {
		return out
	}
	now := s.Now()
	for _, e := range exts {
		if !e.Enabled {
			continue
		}
		if !force && e.LastSyncAt != nil && now.Sub(*e.LastSyncAt) < time.Duration(e.SyncIntervalMin)*time.Minute {
			continue
		}
		if _, err := s.SyncExternal(ctx, e); err != nil {
			out[e.ID] = err
		}
	}
	return out
}
