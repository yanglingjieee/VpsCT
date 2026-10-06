package api

import (
	"net/http"
	"strings"

	"ctlvps/internal/domain"
	"ctlvps/internal/httpx"
	"ctlvps/internal/store"
)

// LineView adds what the menu needs to show a line without further lookups.
type LineView struct {
	domain.Line
	EntryName     string `json:"entry_name"`
	EntryServer   string `json:"entry_server"`
	LandingName   string `json:"landing_name,omitempty"`
	LandingServer string `json:"landing_server,omitempty"`
	// Problem is set when a node of the line can no longer be shared.
	Problem string `json:"problem,omitempty"`
	Users   int    `json:"users"`
}

type lineNode struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	ServerID   int64  `json:"server_id"`
	ServerName string `json:"server_name"`
	ListenPort int    `json:"listen_port"`
	SNI        string `json:"sni"`
}

func (a *API) lineViews(r *http.Request, lines []domain.Line) []LineView {
	ctx := r.Context()
	servers := map[int64]string{}
	if list, err := a.Store.ListServers(ctx); err == nil {
		for _, s := range list {
			servers[s.ID] = s.Name
		}
	}
	shares, _ := a.Store.ListShares(ctx, nil)
	describe := func(id int64) (name, server, problem string) {
		n, err := a.Store.GetNode(ctx, id)
		if err != nil {
			return "", "", "节点已删除"
		}
		if n.ServerID != nil {
			server = servers[*n.ServerID]
		}
		if err := store.LineNodeUsable(n); err != nil {
			problem = n.Name + "：" + err.Error()
		} else if !n.Enabled {
			problem = n.Name + "：已停用"
		}
		return n.Name, server, problem
	}
	out := make([]LineView, 0, len(lines))
	for _, l := range lines {
		v := LineView{Line: l}
		v.EntryName, v.EntryServer, v.Problem = describe(l.EntryNodeID)
		if l.LandingNodeID != nil {
			var problem string
			v.LandingName, v.LandingServer, problem = describe(*l.LandingNodeID)
			if v.Problem == "" {
				v.Problem = problem
			}
		}
		for _, sh := range shares {
			if sh.Status == domain.ShareRevoked {
				continue
			}
			if sh.LineMode == domain.ShareLinesAll {
				v.Users++
				continue
			}
			for _, id := range sh.LineIDs {
				if sh.LineMode == domain.ShareLinesSelected && id == l.ID {
					v.Users++
					break
				}
			}
		}
		out = append(out, v)
	}
	return out
}

func (a *API) listLines(w http.ResponseWriter, r *http.Request) error {
	lines, err := a.Store.ListLines(r.Context())
	if err != nil {
		return err
	}
	httpx.OK(w, a.lineViews(r, lines))
	return nil
}

// lineCandidates lists the nodes several users can share.
func (a *API) lineCandidates(w http.ResponseWriter, r *http.Request) error {
	nodes, err := a.Store.ListNodes(r.Context(), store.NodeFilter{Source: domain.NodeDeployed})
	if err != nil {
		return err
	}
	servers := map[int64]string{}
	if list, err := a.Store.ListServers(r.Context()); err == nil {
		for _, s := range list {
			servers[s.ID] = s.Name
		}
	}
	out := []lineNode{}
	for _, n := range nodes {
		if store.LineNodeUsable(n) != nil {
			continue
		}
		out = append(out, lineNode{ID: n.ID, Name: n.Name, ServerID: *n.ServerID, ServerName: servers[*n.ServerID], ListenPort: n.ListenPort, SNI: nodeSNI(n)})
	}
	httpx.OK(w, out)
	return nil
}

type lineInput struct {
	Name          string `json:"name"`
	EntryNodeID   int64  `json:"entry_node_id"`
	LandingNodeID *int64 `json:"landing_node_id"`
	SortOrder     int    `json:"sort_order"`
	Enabled       *bool  `json:"enabled"`
}

func (in lineInput) apply(l *domain.Line) {
	l.Name = strings.TrimSpace(in.Name)
	l.EntryNodeID = in.EntryNodeID
	l.LandingNodeID = in.LandingNodeID
	if l.LandingNodeID != nil && *l.LandingNodeID == 0 {
		l.LandingNodeID = nil
	}
	l.SortOrder = in.SortOrder
	if in.Enabled != nil {
		l.Enabled = *in.Enabled
	}
}

// syncLines brings every line-following user in step with the lines and the
// nodes behind them. The change that triggered it is already saved, so a
// failure here is reported, not rolled back: the next change retries it.
func (a *API) syncLines(r *http.Request) {
	if err := a.Shares.SyncLines(r.Context()); err != nil {
		a.Logger.Warn("line credentials not fully reconciled", "err", err)
	}
	a.Events.Publish("share.changed", map[string]any{})
}

func (a *API) createLine(w http.ResponseWriter, r *http.Request) error {
	var in lineInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	l := domain.Line{Enabled: true}
	in.apply(&l)
	if err := a.Store.CreateLine(r.Context(), &l); err != nil {
		return httpx.BadRequest(err.Error())
	}
	a.syncLines(r)
	a.audit(r, "line.create", l.Name, nil)
	httpx.JSON(w, http.StatusCreated, a.lineViews(r, []domain.Line{l})[0])
	return nil
}

func (a *API) updateLine(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		return err
	}
	l, err := a.Store.GetLine(r.Context(), id)
	if err != nil {
		return httpx.ErrNotFound
	}
	var in lineInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.apply(&l)
	if err := a.Store.UpdateLine(r.Context(), &l); err != nil {
		return httpx.BadRequest(err.Error())
	}
	a.syncLines(r)
	a.audit(r, "line.update", l.Name, nil)
	httpx.OK(w, a.lineViews(r, []domain.Line{l})[0])
	return nil
}

func (a *API) deleteLine(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		return err
	}
	l, err := a.Store.GetLine(r.Context(), id)
	if err != nil {
		return httpx.ErrNotFound
	}
	if err := a.Store.DeleteLine(r.Context(), id); err != nil {
		return err
	}
	a.syncLines(r)
	a.audit(r, "line.delete", l.Name, nil)
	httpx.NoContent(w)
	return nil
}

func (a *API) reorderLines(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	for i, id := range in.IDs {
		l, err := a.Store.GetLine(r.Context(), id)
		if err != nil {
			continue
		}
		if l.SortOrder != i+1 {
			l.SortOrder = i + 1
			if err := a.Store.UpdateLine(r.Context(), &l); err != nil {
				return httpx.BadRequest(err.Error())
			}
		}
	}
	a.audit(r, "line.reorder", "", nil)
	return a.listLines(w, r)
}
