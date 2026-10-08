package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ctlvps/internal/agentproto"
	"ctlvps/internal/auth"
	"ctlvps/internal/domain"
	"ctlvps/internal/httpx"
	"ctlvps/internal/liveproto"
	"ctlvps/internal/store"

	"golang.org/x/net/websocket"
)

// agentLive is the controller's end of an agent's live channel: the request
// becomes a WebSocket that stays open for as long as the worker runs.
func (a *API) agentLive(w http.ResponseWriter, r *http.Request) error {
	if a.Live == nil {
		return httpx.ErrNotFound
	}
	ac := agentFrom(r.Context())
	hash := auth.HashToken(strings.TrimSpace(strings.TrimPrefix(r.Header.Get(agentproto.AuthHeader), "Bearer ")))
	serverID := ac.Server.ID
	// A token reset or a deleted server ends the connection within a minute.
	valid := func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ag, err := a.Store.GetAgentByTokenHash(ctx, hash)
		return err == nil && ag.ServerID == serverID
	}
	websocket.Server{
		// The agent's token admitted the request; browsers do not come here.
		Handshake: func(*websocket.Config, *http.Request) error { return nil },
		Handler: func(ws *websocket.Conn) {
			// The deadlines the request arrived with do not apply to a stream.
			_ = ws.SetDeadline(time.Time{})
			a.Live.Serve(serverID, ws, valid)
		},
	}.ServeHTTP(w, r)
	return nil
}

func (a *API) liveSnapshot(w http.ResponseWriter, r *http.Request) error {
	httpx.OK(w, a.Live.Snapshot())
	return nil
}

// liveRanges are the spans a server's history can be asked for: which
// resolution each is read from and how wide a point is, in seconds.
var liveRanges = map[string]struct {
	span time.Duration
	res  int
	step int64
}{
	"1h":  {time.Hour, store.LiveMinute, 60},
	"6h":  {6 * time.Hour, store.LiveMinute, 120},
	"24h": {24 * time.Hour, store.LiveMinute, 300},
	"7d":  {7 * 24 * time.Hour, store.LiveHour, 3600},
	"30d": {30 * 24 * time.Hour, store.LiveHour, 4 * 3600},
}

func (a *API) serverHistory(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		return err
	}
	rg, ok := liveRanges[r.URL.Query().Get("range")]
	if !ok {
		return httpx.BadRequest("range 无效")
	}
	to := a.Store.Now()
	from := to.Add(-rg.span)
	points, err := a.Store.ServerMetrics(r.Context(), id, rg.res, from, to, rg.step)
	if err != nil {
		return err
	}
	httpx.OK(w, map[string]any{"from": from.Unix(), "to": to.Unix(), "step": rg.step, "points": points})
	return nil
}

// latencyPoint is one bucket of one target's probes, times in microseconds.
type latencyPoint struct {
	TS   int64 `json:"ts"`
	Sent int   `json:"sent"`
	Lost int   `json:"lost"`
	Avg  int64 `json:"avg"`
	Min  int64 `json:"min"`
	Max  int64 `json:"max"`
}

type latencySeries struct {
	domain.ProbeTarget
	latencyPoint                // the whole span
	Points       []latencyPoint `json:"points"`
}

func (a *API) serverLatency(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		return err
	}
	rg, ok := liveRanges[r.URL.Query().Get("range")]
	if !ok {
		return httpx.BadRequest("range 无效")
	}
	to := a.Store.Now()
	from := to.Add(-rg.span)
	targets, err := a.Store.ListProbeTargets(r.Context())
	if err != nil {
		return err
	}
	rows, err := a.Store.ProbeStats(r.Context(), id, rg.res, from, to, rg.step)
	if err != nil {
		return err
	}
	point := func(p store.ProbePoint) latencyPoint {
		return latencyPoint{TS: p.TS, Sent: p.Sent, Lost: p.Lost, Avg: p.Avg(), Min: p.RTTMin, Max: p.RTTMax}
	}
	out := make([]latencySeries, 0, len(targets))
	for _, t := range targets {
		series := latencySeries{ProbeTarget: t, Points: []latencyPoint{}}
		var whole store.ProbePoint
		for _, p := range rows {
			if p.TargetID == t.ID {
				series.Points = append(series.Points, point(p))
				whole.Merge(p)
			}
		}
		series.latencyPoint = point(whole)
		series.latencyPoint.TS = from.Unix()
		out = append(out, series)
	}
	httpx.OK(w, map[string]any{"from": from.Unix(), "to": to.Unix(), "step": rg.step, "targets": out})
	return nil
}

// ---- probe targets ----

type probeSettings struct {
	Interval int                  `json:"interval"` // seconds between two probes of one target
	Targets  []domain.ProbeTarget `json:"targets"`
}

func (a *API) probeSettings(ctx context.Context) (probeSettings, error) {
	targets, err := a.Store.ListProbeTargets(ctx)
	if err != nil {
		return probeSettings{}, err
	}
	return probeSettings{Interval: a.Store.GetSettingInt(ctx, domain.SettingProbeInterval, 10), Targets: targets}, nil
}

func (a *API) getProbeTargets(w http.ResponseWriter, r *http.Request) error {
	out, err := a.probeSettings(r.Context())
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

func (a *API) putProbeTargets(w http.ResponseWriter, r *http.Request) error {
	var in probeSettings
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Interval < liveproto.MinProbeIntervalSec || in.Interval > liveproto.MaxProbeIntervalSec {
		return httpx.BadRequest("探测间隔必须在 5–300 秒之间")
	}
	if len(in.Targets) > liveproto.MaxTargets {
		return httpx.BadRequest("探测目标最多 24 个")
	}
	seen := map[string]bool{}
	for i := range in.Targets {
		t := &in.Targets[i]
		t.Name, t.Host = strings.TrimSpace(t.Name), strings.ToLower(strings.TrimSpace(t.Host))
		if t.Name == "" || len(t.Name) > 40 {
			return httpx.BadRequest("每个探测目标都要有名称（40 字以内）")
		}
		if err := liveproto.ValidateTarget(t.Host, t.Port); err != nil {
			return httpx.BadRequest("“" + t.Name + "”的地址无效：要公网的域名或 IP，端口 1–65535")
		}
		switch t.Carrier {
		case "", "ct", "cu", "cm":
		default:
			return httpx.BadRequest("运营商无效")
		}
		key := t.Host + ":" + strconv.Itoa(t.Port)
		if seen[key] {
			return httpx.BadRequest("探测目标重复：" + key)
		}
		seen[key] = true
	}
	if _, err := a.Store.ReplaceProbeTargets(r.Context(), in.Targets); err != nil {
		return err
	}
	if err := a.Store.SetSetting(r.Context(), domain.SettingProbeInterval, strconv.Itoa(in.Interval)); err != nil {
		return err
	}
	if err := a.Live.Reload(r.Context()); err != nil {
		return err
	}
	a.audit(r, "probe.targets", "", map[string]any{"targets": len(in.Targets), "interval": in.Interval})
	return a.getProbeTargets(w, r)
}
