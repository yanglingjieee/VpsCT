package report

import (
	"context"
	"time"

	"ctlvps/internal/agentproto"
	"ctlvps/internal/domain"
	"ctlvps/internal/store"
	"ctlvps/internal/traffic"
)

// scene is the network as it stands, read once for the message at hand.
type scene struct {
	now     time.Time // in the panel's timezone
	servers []domain.Server
	server  map[int64]domain.Server
	agent   map[int64]domain.Agent // by server
	lines   []domain.Line          // enabled lines, in menu order
	named   map[int64]string       // every line's name, enabled or not
	hosts   map[int64][]int64      // line → the servers it runs through, entry first
	silence time.Duration          // how long without a heartbeat counts as lost
}

func (r *Reporter) look(ctx context.Context) (*scene, error) {
	sc := &scene{now: r.Store.Now().In(r.Store.Location()), server: map[int64]domain.Server{}, agent: map[int64]domain.Agent{}, hosts: map[int64][]int64{}, named: map[int64]string{}}
	sc.silence = time.Duration(r.Store.GetSettingInt(ctx, domain.SettingAgentOfflineSec, 120)) * time.Second
	var err error
	if sc.servers, err = r.Store.ListServers(ctx); err != nil {
		return nil, err
	}
	for _, s := range sc.servers {
		sc.server[s.ID] = s
	}
	agents, err := r.Store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	for _, ag := range agents {
		sc.agent[ag.ServerID] = ag
	}
	lines, err := r.Store.ListLines(ctx)
	if err != nil {
		return nil, err
	}
	inbounds, err := r.Store.ListNodes(ctx, store.NodeFilter{Source: domain.NodeDeployed, NoMembers: true, IncludeRevoked: true})
	if err != nil {
		return nil, err
	}
	where := map[int64]int64{}
	for _, n := range inbounds {
		if n.ServerID != nil {
			where[n.ID] = *n.ServerID
		}
	}
	for _, l := range lines {
		sc.named[l.ID] = l.Name
		if !l.Enabled {
			continue
		}
		sc.lines = append(sc.lines, l)
		if id, ok := where[l.EntryNodeID]; ok {
			sc.hosts[l.ID] = append(sc.hosts[l.ID], id)
		}
		if l.LandingNodeID != nil {
			if id, ok := where[*l.LandingNodeID]; ok {
				sc.hosts[l.ID] = append(sc.hosts[l.ID], id)
			}
		}
	}
	return sc, nil
}

// enrolled reports whether a server has an agent that has reported at least
// once: only such a server can be lost.
func (sc *scene) enrolled(id int64) bool {
	ag, ok := sc.agent[id]
	return ok && ag.TokenHash != "" && ag.LastSeenAt != nil
}

// lost reports whether an enrolled server has stopped reporting.
func (sc *scene) lost(id int64) bool {
	return sc.enrolled(id) && sc.now.Sub(*sc.agent[id].LastSeenAt) > sc.silence
}

// down reports whether lines through a server cannot be counted on.
func (sc *scene) down(id int64) bool {
	s, ok := sc.server[id]
	return !ok || !s.Enabled || s.QuotaStopped || sc.lost(id)
}

func (sc *scene) metrics(id int64) agentproto.Metrics {
	return traffic.MetricsFromAgent(sc.agent[id])
}

// through lists the lines that run through any of the servers.
func (sc *scene) through(ids ...int64) []string {
	var out []string
	for _, l := range sc.lines {
		if sc.touches(l, ids) {
			out = append(out, l.Name)
		}
	}
	return out
}

// working lists the lines that run through none of the servers and through
// no server that is down.
func (sc *scene) working(except ...int64) []string {
	var out []string
	for _, l := range sc.lines {
		ok := len(sc.hosts[l.ID]) > 0 && !sc.touches(l, except)
		for _, id := range sc.hosts[l.ID] {
			ok = ok && !sc.down(id)
		}
		if ok {
			out = append(out, l.Name)
		}
	}
	return out
}

func (sc *scene) touches(l domain.Line, ids []int64) bool {
	for _, host := range sc.hosts[l.ID] {
		for _, id := range ids {
			if host == id {
				return true
			}
		}
	}
	return false
}

// routes says which lines trouble on the servers takes with it and which
// lines are left to switch to.
func (sc *scene) routes(heading string, ids ...int64) []string {
	hit := sc.through(ids...)
	if len(hit) == 0 {
		return []string{"没有线路经过它，不影响上网。"}
	}
	out := append([]string{"", heading}, bullets(hit)...)
	if left := sc.working(ids...); len(left) > 0 {
		out = append(append(out, "现在能用的线路："), bullets(left)...)
	} else {
		out = append(out, "其余线路现在也都用不了。")
	}
	return out
}
