package agentlive_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ctlvps/internal/agentlive"
	"ctlvps/internal/domain"
	"ctlvps/internal/live"
	"ctlvps/internal/liveproto"
	"ctlvps/internal/store"

	"golang.org/x/net/websocket"
)

// The worker, the hub and the store together: what a host says about itself
// and how long its probes take reach the panel as they happen and the
// database by the minute.
func TestWorkerStreamsToTheHub(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := domain.Server{Name: "入口机", Enabled: true}
	if err := st.CreateServer(ctx, &srv); err != nil {
		t.Fatal(err)
	}

	// One target answers; the other is a port nothing listens on.
	answering, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer answering.Close()
	go func() {
		for {
			c, err := answering.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	silentPort := silent.Addr().(*net.TCPAddr).Port
	silent.Close()
	agentlive.ProbeAnywhere()
	targets, err := st.ReplaceProbeTargets(ctx, []domain.ProbeTarget{
		{Name: "四川电信", Host: "sc-ct.probe.example.com", Port: answering.Addr().(*net.TCPAddr).Port, Carrier: "ct", OnCard: true},
		{Name: "四川联通", Host: "sc-cu.probe.example.com", Port: silentPort, Carrier: "cu", OnCard: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, domain.SettingProbeInterval, "5"); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var events []live.Event
	hub, err := live.New(ctx, st, slog.New(slog.NewTextHandler(io.Discard, nil)), func(typ string, data any) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, data.(live.Event))
	})
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 10, 8, 12, 0, 30, 0, time.UTC)
	hub.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	}
	done := make(chan struct{})
	go func() { hub.Run(ctx); close(done) }()
	controller := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != liveproto.Path || r.Header.Get("Authorization") != "Bearer agent-token" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(ws *websocket.Conn) {
			hub.Serve(srv.ID, ws, func() bool { return true })
		}}.ServeHTTP(w, r)
	}))
	defer controller.Close()
	pool := x509.NewCertPool()
	pool.AddCert(controller.Certificate())

	// Somebody is watching: the worker is asked for a reading every second.
	leave := hub.Watch()
	go agentlive.Run(ctx, agentlive.Config{URL: controller.URL, Token: "agent-token", Version: "test", TLS: &tls.Config{RootCAs: pool}})

	wait := func(what string, ok func(live.Snapshot) bool) live.Snapshot {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if snap := hub.Snapshot(); ok(snap) {
				return snap
			}
		}
		t.Fatalf("timed out waiting for %s: %+v", what, hub.Snapshot())
		return live.Snapshot{}
	}
	snap := wait("two readings and both probes", func(s live.Snapshot) bool {
		v := s.Servers[srv.ID]
		return v.Connected && len(v.Trail) >= 2 && len(v.Probes) == 2 && v.Probes[0].RTT != 0 && v.Probes[1].RTT != 0
	})
	v := snap.Servers[srv.ID]
	if snap.IntervalMs != liveproto.WatchedIntervalMs || v.Host == nil || v.Host.Cores < 1 || v.Sample == nil {
		t.Fatalf("snapshot: %+v", snap)
	}
	if v.Probes[0].TargetID != targets[0].ID || v.Probes[0].RTT <= 0 || v.Probes[1].RTT != liveproto.Lost {
		t.Fatalf("an answered probe has a round trip and a refused one is lost: %+v", v.Probes)
	}
	if got := v.Probes[1].Strip; len(got) != 1 || got[0].Sent < 1 || got[0].Lost != got[0].Sent {
		t.Fatalf("the running minute of the lost target: %+v", got)
	}
	if hosts, _ := st.AgentHosts(ctx); hosts[srv.ID].Cores != v.Host.Cores {
		t.Fatalf("what the host is must be kept: %+v", hosts)
	}
	mu.Lock()
	told := false
	for _, e := range events {
		told = told || e.Connected[srv.ID]
	}
	mu.Unlock()
	if !told {
		t.Fatalf("open panels must be told the server is sending: %+v", events)
	}

	// The minute ends: what it came to is stored, by the minute and the hour.
	mu.Lock()
	clock = clock.Add(time.Minute)
	mu.Unlock()
	var minutes []store.MetricPoint
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && len(minutes) == 0; time.Sleep(100 * time.Millisecond) {
		minutes, _ = st.ServerMetrics(ctx, srv.ID, store.LiveMinute, clock.Add(-time.Hour), clock.Add(time.Hour), 60)
	}
	if len(minutes) != 1 || minutes[0].TS != time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC).Unix() || minutes[0].N < 2 {
		t.Fatalf("the finished minute: %+v", minutes)
	}
	hours, _ := st.ProbeStats(ctx, srv.ID, store.LiveHour, clock.Add(-2*time.Hour), clock.Add(time.Hour), 3600)
	if len(hours) != 2 {
		t.Fatalf("each target has its hour: %+v", hours)
	}
	for _, h := range hours {
		if lost := h.TargetID == targets[1].ID; h.Sent < 1 || (lost && h.Lost != h.Sent) || (!lost && (h.Lost != 0 || h.Avg() <= 0)) {
			t.Fatalf("hour of target %d: %+v", h.TargetID, h)
		}
	}
	if w := hub.Window(10); w[store.ProbeKey{ServerID: srv.ID, TargetID: targets[1].ID}].Lost < 1 {
		t.Fatalf("the finished minute is what the quality job judges: %+v", w)
	}

	// A target taken off the list is no longer probed or shown.
	if _, err := st.ReplaceProbeTargets(ctx, targets[:1]); err != nil {
		t.Fatal(err)
	}
	if err := hub.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	wait("one target", func(s live.Snapshot) bool { return len(s.Targets) == 1 && len(s.Servers[srv.ID].Probes) == 1 })

	// Nobody watches any more; the worker's connection ends with the worker.
	leave()
	cancel()
	<-done
}

func TestProbeTargetsAreHeldToThePublicInternet(t *testing.T) {
	for host, ok := range map[string]bool{
		"sc-ct-v4.ip.zstaticcdn.com": true, "1.1.1.1": true, "2606:4700:4700::1111": true,
		"localhost": false, "127.0.0.1": false, "10.0.0.8": false, "192.168.31.2": false, "169.254.169.254": false,
		"100.100.100.200": false, "::1": false, "fd00::1": false, "bad host.example.com": false, "-a.example.com": false, "": false,
	} {
		if err := liveproto.ValidateTarget(host, 80); (err == nil) != ok {
			t.Errorf("%q: want accepted=%v, got %v", host, ok, err)
		}
	}
	plan := liveproto.Plan{IntervalSec: 10, Targets: []liveproto.Target{{ID: 1, Host: "a.example.com", Port: 80}, {ID: 1, Host: "b.example.com", Port: 80}}}
	if plan.Validate() == nil {
		t.Error("two targets cannot share an id")
	}
	plan.Targets[1].ID, plan.IntervalSec = 2, 1
	if plan.Validate() == nil {
		t.Error("an interval under five seconds is refused")
	}
}
