package api

import (
	"context"
	"net/http"

	"ctlvps/internal/domain"
	"ctlvps/internal/proxynode"
	"ctlvps/internal/store"
)

// LineUsage is one of a user's lines with what each machine carried for them
// in the current period.
type LineUsage struct {
	LineID        int64  `json:"line_id"`
	Name          string `json:"name"`
	EntryServer   string `json:"entry_server"`
	LandingServer string `json:"landing_server,omitempty"`
	EntryUp       int64  `json:"entry_up"`
	EntryDown     int64  `json:"entry_down"`
	LandingUp     int64  `json:"landing_up"`
	LandingDown   int64  `json:"landing_down"`
	Total         int64  `json:"total"`
	// Ready is false while a credential of the line is missing or refused.
	Ready bool `json:"ready"`
}

// PersonNode is a node handed to a user directly.
type PersonNode struct {
	Name string `json:"name"`
	URI  string `json:"uri"`
}

// shareLines lists a user's lines in menu order with per-machine usage.
func (a *API) shareLines(ctx context.Context, sh domain.Share) []LineUsage {
	out := []LineUsage{}
	lines, err := a.Store.ShareLines(ctx, sh)
	if err != nil {
		return out
	}
	members, _ := a.Store.ListNodes(ctx, store.NodeFilter{ShareID: &sh.ID})
	type key struct {
		line    int64
		landing bool
	}
	own := map[key]domain.Node{}
	for _, n := range members {
		if n.LineID != nil {
			own[key{*n.LineID, n.Landing}] = n
		}
	}
	servers := map[int64]string{}
	if list, err := a.Store.ListServers(ctx); err == nil {
		for _, s := range list {
			servers[s.ID] = s.Name
		}
	}
	serverOf := func(nodeID int64) string {
		if n, err := a.Store.GetNode(ctx, nodeID); err == nil && n.ServerID != nil {
			return servers[*n.ServerID]
		}
		return ""
	}
	now := a.Store.Now()
	sum := func(n domain.Node, ok bool) (int64, int64) {
		if !ok {
			return 0, 0
		}
		up, down, _ := a.Store.SumTraffic(ctx, store.SubjectNode, n.ID, sh.PeriodStart, now)
		return up, down
	}
	for _, l := range lines {
		u := LineUsage{LineID: l.ID, Name: l.Name, EntryServer: serverOf(l.EntryNodeID)}
		entry, ok := own[key{l.ID, false}]
		u.Ready = ok
		u.EntryUp, u.EntryDown = sum(entry, ok)
		if l.LandingNodeID != nil {
			u.LandingServer = serverOf(*l.LandingNodeID)
			landing, ok := own[key{l.ID, true}]
			u.Ready = u.Ready && ok
			u.LandingUp, u.LandingDown = sum(landing, ok)
		}
		u.Total = u.EntryUp + u.EntryDown + u.LandingUp + u.LandingDown
		out = append(out, u)
	}
	return out
}

// shareNodes renders a user's nodes as share links, in menu order.
func (a *API) shareNodes(ctx context.Context, sub domain.Subscription) []PersonNode {
	out := []PersonNode{}
	b, err := a.Subs.Build(ctx, sub)
	if err != nil {
		return out
	}
	byName := map[string]proxynode.Proxy{}
	for _, p := range b.Proxies {
		byName[p.Name] = p
	}
	for _, name := range b.AllProxyNames() {
		if p, ok := byName[name]; ok {
			if uri, err := proxynode.ToURI(p); err == nil && uri != "" {
				out = append(out, PersonNode{Name: name, URI: uri})
			}
		}
	}
	return out
}

// PersonalPage is what a user sees when opening their link in a browser.
type PersonalPage struct {
	SiteName  string             `json:"site_name"`
	Name      string             `json:"name"`
	Status    domain.ShareStatus `json:"status"`
	Delivery  string             `json:"delivery"`
	Upload    int64              `json:"upload"`
	Download  int64              `json:"download"`
	Used      int64              `json:"used"`
	Quota     int64              `json:"quota"`
	NextReset string             `json:"next_reset,omitempty"`
	ExpiresAt string             `json:"expires_at,omitempty"`
	Lines     []LineUsage        `json:"lines"`
	Formats   []string           `json:"formats"`
	Rules     string             `json:"rules"`
	Nodes     []PersonNode       `json:"nodes,omitempty"`
	// URL is the address clients subscribe to: the one that was opened.
	URL string `json:"url"`
}

func (a *API) personalPage(r *http.Request, sub domain.Subscription, sh domain.Share) PersonalPage {
	ctx := r.Context()
	u := a.Shares.UsageOf(sh)
	p := PersonalPage{
		SiteName: a.Store.GetSetting(ctx, domain.SettingSiteName, defaultSiteName),
		Name:     sh.Name, Status: sh.Status, Delivery: sh.Delivery,
		Upload: sh.UsedUpload, Download: sh.UsedDownload, Used: u.Used, Quota: sh.QuotaBytes,
		Lines: a.shareLines(ctx, sh), Formats: a.Subs.ShareFormats(ctx, sh), Rules: "无规则",
		URL: a.baseURL(r) + r.URL.Path,
	}
	// Users see names and totals, not which of the operator's machines
	// carried what.
	for i := range p.Lines {
		p.Lines[i] = LineUsage{Name: p.Lines[i].Name, Total: p.Lines[i].Total, Ready: p.Lines[i].Ready}
	}
	if u.NextReset != nil {
		p.NextReset = u.NextReset.Format("2006-01-02T15:04:05Z07:00")
	}
	if sh.ExpiresAt != nil {
		p.ExpiresAt = sh.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if sh.RulesetID != nil && sh.Delivery != domain.DeliveryNodes {
		if rs, err := a.Store.GetRuleset(ctx, *sh.RulesetID); err == nil {
			p.Rules = rs.Name
		}
	}
	if sh.Delivery == domain.DeliveryNodes && sh.Status == domain.ShareActive {
		p.Nodes = a.shareNodes(ctx, sub)
	}
	return p
}
