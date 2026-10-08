// Package live is the controller's end of the live channel. It holds what
// every server's worker last said, hands it to whoever is looking at the
// panel as it arrives, and keeps what it came to by the minute. Readings
// arrive every second while somebody is looking and every five when nobody
// is; none of them touches the database on its own.
package live

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"ctlvps/internal/domain"
	"ctlvps/internal/liveproto"
	"ctlvps/internal/store"

	"golang.org/x/net/websocket"
)

const (
	// A connection that has said nothing for this long is gone.
	readTimeout  = 30 * time.Second
	writeTimeout = 5 * time.Second
	keepalive    = 25 * time.Second
	// Minutes of each probe a card draws.
	StripMinutes = 30
	// Recent readings kept for the little charts beside the live numbers.
	TrailLength = 120
)

// Store is what the hub keeps in the database.
type Store interface {
	ListProbeTargets(ctx context.Context) ([]domain.ProbeTarget, error)
	GetSettingInt(ctx context.Context, key string, def int) int
	SetAgentHost(ctx context.Context, serverID int64, h liveproto.Host) error
	AgentHosts(ctx context.Context) (map[int64]liveproto.Host, error)
	AddLiveMinutes(ctx context.Context, metrics []store.MetricPoint, probes []store.ProbePoint) error
	ProbeStats(ctx context.Context, serverID int64, res int, from, to time.Time, step int64) ([]store.ProbePoint, error)
}

// Hub is the live state of every server. Its methods are safe on a nil Hub,
// which holds nothing.
type Hub struct {
	Store  Store
	Logger *slog.Logger
	// Publish hands an event to the open panels.
	Publish func(typ string, data any)
	Now     func() time.Time
	// Revalidate is how often a connection's agent is checked to still be
	// allowed to speak for its server.
	Revalidate time.Duration
	// Linger is how long after the last viewer left the servers keep
	// sending every second: a reload should not slow them down and speed
	// them up again.
	Linger time.Duration

	mu       sync.Mutex
	servers  map[int64]*server
	targets  map[int64]domain.ProbeTarget
	plan     liveproto.Plan
	viewers  int
	lastView time.Time
	interval int

	// Finished minutes waiting to be stored, and news waiting to be told.
	doneMetrics []store.MetricPoint
	doneProbes  []store.ProbePoint
	news        Event
}

type server struct {
	conn *conn
	// told: the open panels have been told this server is sending.
	told   bool
	host   *liveproto.Host
	last   *liveproto.Sample
	lastAt time.Time
	trail  []Trail
	minute metricAcc
	probes map[int64]*probe
}

type probe struct {
	last   int64 // microseconds; liveproto.Lost
	lastAt time.Time
	cur    store.ProbePoint
	strip  []store.ProbePoint // finished minutes, oldest first
}

// Trail is one recent reading, cut down to what the little charts draw.
type Trail struct {
	TS  int64   `json:"t"` // unix milliseconds, by the controller's clock
	Rx  int64   `json:"rx"`
	Tx  int64   `json:"tx"`
	CPU float64 `json:"c"`
}

type conn struct {
	ws   *websocket.Conn
	send chan liveproto.Down
	done chan struct{}
	once sync.Once
}

func (c *conn) close() {
	c.once.Do(func() {
		close(c.done)
		c.ws.Close()
	})
}

// tell queues a message; a connection too slow to take it is dropped, and the
// worker comes back for the current state.
func (c *conn) tell(d liveproto.Down) {
	select {
	case c.send <- d:
	default:
		c.close()
	}
}

func (c *conn) write() {
	t := time.NewTicker(keepalive)
	defer t.Stop()
	for {
		var d liveproto.Down
		select {
		case <-c.done:
			return
		case d = <-c.send:
		case <-t.C:
		}
		b, _ := json.Marshal(d)
		_ = c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
		if websocket.Message.Send(c.ws, string(b)) != nil {
			c.close()
			return
		}
	}
}

// New builds a hub and loads what it needs to carry on where the last run of
// the panel stopped: the targets, and the last half hour of each probe.
func New(ctx context.Context, st Store, logger *slog.Logger, publish func(string, any)) (*Hub, error) {
	h := &Hub{Store: st, Logger: logger, Publish: publish, Now: func() time.Time { return time.Now().UTC() }, Revalidate: time.Minute, Linger: 20 * time.Second,
		servers: map[int64]*server{}, targets: map[int64]domain.ProbeTarget{}, interval: liveproto.IdleIntervalMs}
	if err := h.Reload(ctx); err != nil {
		return nil, err
	}
	now := h.Now()
	recent, err := st.ProbeStats(ctx, 0, store.LiveMinute, now.Add(-StripMinutes*time.Minute), now, store.LiveMinute)
	if err != nil {
		return nil, err
	}
	hosts, err := st.AgentHosts(ctx)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	minute := now.Unix() - now.Unix()%store.LiveMinute
	for _, p := range recent {
		// The running minute is rebuilt from what arrives now.
		if _, ok := h.targets[p.TargetID]; ok && p.TS < minute {
			pr := h.probe(h.server(p.ServerID), p.TargetID)
			pr.strip = append(pr.strip, p)
		}
	}
	for id, host := range hosts {
		host := host
		h.server(id).host = &host
	}
	return h, nil
}

// Reload takes the probe targets and their interval from the store and tells
// every connected worker. Call it after either changes.
func (h *Hub) Reload(ctx context.Context) error {
	if h == nil {
		return nil
	}
	list, err := h.Store.ListProbeTargets(ctx)
	if err != nil {
		return err
	}
	every := max(liveproto.MinProbeIntervalSec, min(liveproto.MaxProbeIntervalSec, h.Store.GetSettingInt(ctx, domain.SettingProbeInterval, 10)))
	h.mu.Lock()
	defer h.mu.Unlock()
	h.targets = map[int64]domain.ProbeTarget{}
	h.plan = liveproto.Plan{IntervalSec: every, Targets: []liveproto.Target{}}
	for _, t := range list {
		h.targets[t.ID] = t
		h.plan.Targets = append(h.plan.Targets, liveproto.Target{ID: t.ID, Host: t.Host, Port: t.Port})
	}
	for _, s := range h.servers {
		for id := range s.probes {
			if _, ok := h.targets[id]; !ok {
				delete(s.probes, id)
			}
		}
		if s.conn != nil {
			plan := h.plan
			s.conn.tell(liveproto.Down{Plan: &plan})
		}
	}
	return nil
}

func (h *Hub) server(id int64) *server {
	s := h.servers[id]
	if s == nil {
		s = &server{probes: map[int64]*probe{}}
		h.servers[id] = s
	}
	return s
}

func (h *Hub) probe(s *server, target int64) *probe {
	p := s.probes[target]
	if p == nil {
		p = &probe{}
		s.probes[target] = p
	}
	return p
}

// Forget drops a server that no longer exists.
func (h *Hub) Forget(serverID int64) {
	if h == nil {
		return
	}
	h.mu.Lock()
	s := h.servers[serverID]
	delete(h.servers, serverID)
	h.mu.Unlock()
	if s != nil && s.conn != nil {
		s.conn.close()
	}
}

// Serve runs one worker's connection until it ends. valid is asked now and
// then whether the agent may still speak for the server.
func (h *Hub) Serve(serverID int64, ws *websocket.Conn, valid func() bool) {
	if h == nil {
		return
	}
	ws.MaxPayloadBytes = liveproto.MaxUpBytes
	c := &conn{ws: ws, send: make(chan liveproto.Down, 8), done: make(chan struct{})}
	defer c.close()

	h.mu.Lock()
	s := h.server(serverID)
	if s.conn != nil {
		s.conn.close()
	}
	s.conn = c
	plan := h.plan
	c.tell(liveproto.Down{IntervalMs: h.interval, Plan: &plan})
	h.mu.Unlock()
	defer h.left(serverID, c)
	go c.write()

	checked := time.Now()
	for {
		_ = ws.SetReadDeadline(time.Now().Add(readTimeout))
		var raw string
		if websocket.Message.Receive(ws, &raw) != nil {
			return
		}
		var up liveproto.Up
		if json.Unmarshal([]byte(raw), &up) != nil {
			return
		}
		if time.Since(checked) > h.Revalidate {
			if !valid() {
				return
			}
			checked = time.Now()
		}
		h.take(serverID, c, up)
	}
}

func (h *Hub) left(serverID int64, c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.servers[serverID]; s != nil && s.conn == c {
		s.conn = nil
		h.tellConnected(serverID, s, false)
	}
}

func (h *Hub) tellConnected(serverID int64, s *server, on bool) {
	if s.told == on {
		return
	}
	s.told = on
	if h.news.Connected == nil {
		h.news.Connected = map[int64]bool{}
	}
	h.news.Connected[serverID] = on
}

func (h *Hub) take(serverID int64, c *conn, up liveproto.Up) {
	now := h.Now()
	h.mu.Lock()
	s := h.servers[serverID]
	if s == nil || s.conn != c {
		h.mu.Unlock()
		return
	}
	var host *liveproto.Host
	if up.Host != nil && (s.host == nil || *s.host != *up.Host) {
		host = cleanHost(*up.Host)
		s.host = host
	}
	if up.Sample != nil {
		sample := cleanSample(*up.Sample)
		s.last, s.lastAt = &sample, now
		s.trail = append(s.trail, Trail{TS: now.UnixMilli(), Rx: sample.RxRate, Tx: sample.TxRate, CPU: sample.CPU})
		if len(s.trail) > TrailLength {
			s.trail = s.trail[len(s.trail)-TrailLength:]
		}
		minute := now.Unix() - now.Unix()%store.LiveMinute
		if s.minute.n > 0 && s.minute.ts != minute {
			h.doneMetrics = append(h.doneMetrics, s.minute.point(serverID))
			s.minute = metricAcc{}
		}
		s.minute.ts = minute
		s.minute.add(sample)
		if h.news.Samples == nil {
			h.news.Samples = map[int64]liveproto.Sample{}
		}
		h.news.Samples[serverID] = sample
		h.tellConnected(serverID, s, true)
	}
	if len(up.Probes) > liveproto.MaxTargets {
		up.Probes = nil
	}
	for _, r := range up.Probes {
		if _, ok := h.targets[r.Target]; !ok {
			continue
		}
		p := h.probe(s, r.Target)
		h.roll(serverID, r.Target, p, now)
		p.last, p.lastAt = r.RTT, now
		p.cur.Sent++
		if r.RTT <= 0 {
			p.last = liveproto.Lost
			p.cur.Lost++
		} else {
			rtt := min(r.RTT, int64(liveproto.ProbeTimeoutMs)*2000)
			if p.cur.Sent-p.cur.Lost == 1 || rtt < p.cur.RTTMin {
				p.cur.RTTMin = rtt
			}
			p.cur.RTTMax = max(p.cur.RTTMax, rtt)
			p.cur.RTTSum += rtt
			p.last = rtt
		}
		h.news.Probes = append(h.news.Probes, ProbeNews{ServerID: serverID, TargetID: r.Target, RTT: p.last, Minute: p.cur})
	}
	h.mu.Unlock()
	if host != nil {
		if err := h.Store.SetAgentHost(context.Background(), serverID, *host); err != nil {
			h.Logger.Warn("host description not stored", "server_id", serverID, "err", err)
		}
	}
}

// roll moves a probe's minute on when the clock has: the finished minute
// joins the strip and waits to be stored.
func (h *Hub) roll(serverID, targetID int64, p *probe, now time.Time) {
	minute := now.Unix() - now.Unix()%store.LiveMinute
	if p.cur.TS == minute {
		return
	}
	if p.cur.Sent > 0 {
		p.strip = append(p.strip, p.cur)
		h.doneProbes = append(h.doneProbes, p.cur)
	}
	for len(p.strip) > 0 && p.strip[0].TS < minute-StripMinutes*store.LiveMinute {
		p.strip = p.strip[1:]
	}
	p.cur = store.ProbePoint{ServerID: serverID, TargetID: targetID, TS: minute}
}

func cleanHost(h liveproto.Host) *liveproto.Host {
	cut := func(s string, n int) string {
		if len(s) > n {
			return s[:n]
		}
		return s
	}
	return &liveproto.Host{OS: cut(h.OS, 80), Kernel: cut(h.Kernel, 80), Arch: cut(h.Arch, 16), Virt: cut(h.Virt, 24), CPUModel: cut(h.CPUModel, 80), Cores: max(0, min(4096, h.Cores))}
}

// A reading is what a host says about itself: kept within what can be true.
func cleanSample(s liveproto.Sample) liveproto.Sample {
	pos := func(v int64) int64 { return max(0, v) }
	s.CPU = max(0, min(100, s.CPU))
	s.MemTotal, s.MemUsed = pos(s.MemTotal), pos(s.MemUsed)
	s.SwapTotal, s.SwapUsed = pos(s.SwapTotal), pos(s.SwapUsed)
	s.DiskTotal, s.DiskUsed = pos(s.DiskTotal), pos(s.DiskUsed)
	s.RxRate, s.TxRate = pos(s.RxRate), pos(s.TxRate)
	s.Load1, s.Load5, s.Load15 = max(0, s.Load1), max(0, s.Load5), max(0, s.Load15)
	s.TCP, s.UDP = max(0, s.TCP), max(0, s.UDP)
	s.Uptime = pos(s.Uptime)
	return s
}

// metricAcc gathers a server's readings over one minute.
type metricAcc struct {
	ts                                int64
	n                                 int
	cpu, cpuMax, load                 float64
	mem, swap, disk, rx, tx           int64
	memTotal, diskTotal, rxMax, txMax int64
	tcp, udp                          int64
}

func (a *metricAcc) add(s liveproto.Sample) {
	a.n++
	a.cpu += s.CPU
	a.cpuMax = max(a.cpuMax, s.CPU)
	a.load += s.Load1
	a.mem += s.MemUsed
	a.swap += s.SwapUsed
	a.disk += s.DiskUsed
	a.rx += s.RxRate
	a.tx += s.TxRate
	a.rxMax, a.txMax = max(a.rxMax, s.RxRate), max(a.txMax, s.TxRate)
	a.memTotal, a.diskTotal = s.MemTotal, s.DiskTotal
	a.tcp += int64(s.TCP)
	a.udp += int64(s.UDP)
}

func (a *metricAcc) point(serverID int64) store.MetricPoint {
	n := int64(a.n)
	return store.MetricPoint{ServerID: serverID, TS: a.ts, N: a.n, CPU: a.cpu / float64(n), CPUMax: a.cpuMax, MemUsed: a.mem / n, MemTotal: a.memTotal, SwapUsed: a.swap / n,
		DiskUsed: a.disk / n, DiskTotal: a.diskTotal, Load1: a.load / float64(n), RxRate: a.rx / n, TxRate: a.tx / n, RxMax: a.rxMax, TxMax: a.txMax, TCP: int(a.tcp / n), UDP: int(a.udp / n)}
}

// ---- viewers ----

// Watch says somebody opened the panel; call what it returns when they leave.
// While anybody watches, the servers send a reading every second.
func (h *Hub) Watch() (leave func()) {
	if h == nil {
		return func() {}
	}
	h.mu.Lock()
	h.viewers++
	h.pace(liveproto.WatchedIntervalMs)
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			h.viewers--
			h.lastView = h.Now()
			h.mu.Unlock()
		})
	}
}

func (h *Hub) pace(ms int) {
	if h.interval == ms {
		return
	}
	h.interval = ms
	for _, s := range h.servers {
		if s.conn != nil {
			s.conn.tell(liveproto.Down{IntervalMs: ms})
		}
	}
}

// ---- telling and storing ----

// Event is what changed since the last one: the newest reading of each
// server that sent one, each probe that came back, and each server whose
// worker connected or went away.
type Event struct {
	Samples   map[int64]liveproto.Sample `json:"s,omitempty"`
	Probes    []ProbeNews                `json:"p,omitempty"`
	Connected map[int64]bool             `json:"c,omitempty"`
}

// ProbeNews is one probe result with the minute it now belongs to.
type ProbeNews struct {
	ServerID int64            `json:"sid"`
	TargetID int64            `json:"tid"`
	RTT      int64            `json:"us"`
	Minute   store.ProbePoint `json:"m"`
}

// Run tells the open panels what arrived, once a second, and stores finished
// minutes, until ctx ends.
func (h *Hub) Run(ctx context.Context) {
	tell := time.NewTicker(time.Second)
	defer tell.Stop()
	keep := time.NewTicker(5 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-ctx.Done():
			h.flush(context.WithoutCancel(ctx), true)
			return
		case <-tell.C:
			h.mu.Lock()
			news := h.news
			h.news = Event{}
			watched := h.viewers > 0
			if !watched && h.Now().Sub(h.lastView) >= h.Linger {
				h.pace(liveproto.IdleIntervalMs)
			}
			h.mu.Unlock()
			if watched && (len(news.Samples) > 0 || len(news.Probes) > 0 || len(news.Connected) > 0) && h.Publish != nil {
				h.Publish("live", news)
			}
		case <-keep.C:
			h.flush(ctx, false)
		}
	}
}

// flush stores the minutes that are over. A server that went quiet does not
// send the reading that would close its last minute, so the clock closes it;
// with all set, the running minute is stored as far as it got.
func (h *Hub) flush(ctx context.Context, all bool) {
	now := h.Now()
	minute := now.Unix() - now.Unix()%store.LiveMinute
	h.mu.Lock()
	for id, s := range h.servers {
		if s.minute.n > 0 && (all || s.minute.ts != minute) {
			h.doneMetrics = append(h.doneMetrics, s.minute.point(id))
			s.minute = metricAcc{}
		}
		for target, p := range s.probes {
			if all && p.cur.Sent > 0 {
				h.doneProbes = append(h.doneProbes, p.cur)
				continue
			}
			h.roll(id, target, p, now)
		}
	}
	metrics, probes := h.doneMetrics, h.doneProbes
	h.doneMetrics, h.doneProbes = nil, nil
	h.mu.Unlock()
	if err := h.Store.AddLiveMinutes(ctx, metrics, probes); err != nil {
		h.Logger.Warn("live minutes not stored", "err", err)
	}
}

// ---- reading the state ----

// ProbeView is one target as one server sees it now.
type ProbeView struct {
	TargetID int64 `json:"target_id"`
	// RTT of the newest probe in microseconds, liveproto.Lost when it was
	// lost, 0 before the first.
	RTT   int64              `json:"us"`
	At    int64              `json:"at,omitempty"` // unix milliseconds
	Strip []store.ProbePoint `json:"strip"`        // the last half hour by the minute, the running minute last
}

// ServerView is one server as its worker last described it.
type ServerView struct {
	Connected bool              `json:"connected"`
	At        int64             `json:"at,omitempty"` // unix milliseconds of the newest reading
	Sample    *liveproto.Sample `json:"sample,omitempty"`
	Host      *liveproto.Host   `json:"host,omitempty"`
	Trail     []Trail           `json:"trail"`
	Probes    []ProbeView       `json:"probes"`
}

// Snapshot is everything the hub holds, for a panel that just opened.
type Snapshot struct {
	IntervalMs    int                  `json:"interval_ms"`
	ProbeInterval int                  `json:"probe_interval"`
	Targets       []domain.ProbeTarget `json:"targets"`
	Servers       map[int64]ServerView `json:"servers"`
}

// Snapshot returns the current state.
func (h *Hub) Snapshot() Snapshot {
	if h == nil {
		return Snapshot{Targets: []domain.ProbeTarget{}, Servers: map[int64]ServerView{}}
	}
	now := h.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	out := Snapshot{IntervalMs: h.interval, ProbeInterval: h.plan.IntervalSec, Targets: []domain.ProbeTarget{}, Servers: map[int64]ServerView{}}
	for _, t := range h.plan.Targets {
		out.Targets = append(out.Targets, h.targets[t.ID])
	}
	for id, s := range h.servers {
		v := ServerView{Connected: s.told, Host: s.host, Trail: append([]Trail{}, s.trail...), Probes: []ProbeView{}}
		if s.last != nil {
			sample := *s.last
			v.Sample, v.At = &sample, s.lastAt.UnixMilli()
		}
		for _, t := range h.plan.Targets {
			if p := s.probes[t.ID]; p != nil {
				v.Probes = append(v.Probes, h.probeView(id, t.ID, p, now))
			}
		}
		out.Servers[id] = v
	}
	return out
}

func (h *Hub) probeView(serverID, targetID int64, p *probe, now time.Time) ProbeView {
	h.roll(serverID, targetID, p, now)
	v := ProbeView{TargetID: targetID, RTT: p.last, Strip: append([]store.ProbePoint{}, p.strip...)}
	if !p.lastAt.IsZero() {
		v.At = p.lastAt.UnixMilli()
	}
	if p.cur.Sent > 0 {
		v.Strip = append(v.Strip, p.cur)
	}
	return v
}

// Host is what a server's worker says the host is, nil when it never did.
func (h *Hub) Host(serverID int64) *liveproto.Host {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.servers[serverID]; s != nil && s.host != nil {
		host := *s.host
		return &host
	}
	return nil
}

// Window is what each server's probes of each target came to over the last
// minutes that are over.
func (h *Hub) Window(minutes int) map[store.ProbeKey]store.ProbePoint {
	if h == nil {
		return nil
	}
	now := h.Now()
	minute := now.Unix() - now.Unix()%store.LiveMinute
	from := minute - int64(minutes)*store.LiveMinute
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[store.ProbeKey]store.ProbePoint{}
	for id, s := range h.servers {
		for target, p := range s.probes {
			h.roll(id, target, p, now)
			sum := store.ProbePoint{ServerID: id, TargetID: target, TS: from}
			for _, m := range p.strip {
				if m.TS >= from && m.TS < minute {
					sum.Merge(m)
				}
			}
			if sum.Sent > 0 {
				out[store.ProbeKey{ServerID: id, TargetID: target}] = sum
			}
		}
	}
	return out
}
