package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"ctlvps/internal/domain"
)

func TestLiveMeasurementsByMinuteAndHour(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 10, 8, 12, 30, 0, 0, time.UTC)
	st.Now = func() time.Time { return now }
	srv := domain.Server{Name: "s", Enabled: true}
	if err := st.CreateServer(ctx, &srv); err != nil {
		t.Fatal(err)
	}
	targets, err := st.ReplaceProbeTargets(ctx, []domain.ProbeTarget{{Name: "电信", Host: "ct.example.com", Port: 80, Carrier: "ct", OnCard: true}, {Name: "联通", Host: "cu.example.com", Port: 80, Carrier: "cu"}})
	if err != nil || len(targets) != 2 || !targets[0].OnCard || targets[1].OnCard {
		t.Fatalf("targets: %+v %v", targets, err)
	}
	ct, cu := targets[0].ID, targets[1].ID

	hour := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC).Unix()
	metrics := []MetricPoint{
		{ServerID: srv.ID, TS: hour, N: 60, CPU: 10, CPUMax: 30, MemUsed: 100, MemTotal: 1000, RxRate: 1000, TxRate: 2000, RxMax: 5000, TxMax: 6000, TCP: 10},
		{ServerID: srv.ID, TS: hour + 60, N: 20, CPU: 50, CPUMax: 90, MemUsed: 500, MemTotal: 1000, RxRate: 5000, TxRate: 6000, RxMax: 9000, TxMax: 8000, TCP: 50},
		{ServerID: 999, TS: hour, N: 60}, // a server that is gone
	}
	probes := []ProbePoint{
		{ServerID: srv.ID, TargetID: ct, TS: hour, Sent: 6, Lost: 0, RTTSum: 6 * 180_000, RTTMin: 170_000, RTTMax: 190_000},
		{ServerID: srv.ID, TargetID: ct, TS: hour + 60, Sent: 6, Lost: 6},
		{ServerID: srv.ID, TargetID: cu, TS: hour + 60, Sent: 6, Lost: 2, RTTSum: 4 * 250_000, RTTMin: 240_000, RTTMax: 300_000},
	}
	// Stored twice, each minute still counts once in its hour.
	for i := 0; i < 2; i++ {
		if err := st.AddLiveMinutes(ctx, metrics, probes); err != nil {
			t.Fatal(err)
		}
	}
	from, to := now.Add(-time.Hour), now.Add(time.Hour)
	hours, err := st.ServerMetrics(ctx, srv.ID, LiveHour, from, to, 3600)
	if err != nil || len(hours) != 1 {
		t.Fatalf("hours: %+v %v", hours, err)
	}
	// 60 readings at 10% and 20 at 50%: 20% on average, and the peaks kept.
	if h := hours[0]; h.N != 80 || h.CPU != 20 || h.CPUMax != 90 || h.MemUsed != 200 || h.RxRate != 2000 || h.TxMax != 8000 || h.TCP != 20 {
		t.Fatalf("an hour is its minutes weighed by their readings: %+v", h)
	}
	if wide, _ := st.ServerMetrics(ctx, srv.ID, LiveMinute, from, to, 300); len(wide) != 1 || wide[0].N != 80 || wide[0].TS != hour {
		t.Fatalf("minutes gathered into five: %+v", wide)
	}
	stats, err := st.ProbeStats(ctx, srv.ID, LiveHour, from, to, 3600)
	if err != nil || len(stats) != 2 {
		t.Fatalf("probe hours: %+v %v", stats, err)
	}
	for _, p := range stats {
		switch p.TargetID {
		case ct: // a minute in which nothing answered has no shortest round trip to offer
			if p.Sent != 12 || p.Lost != 6 || p.Avg() != 180_000 || p.RTTMin != 170_000 {
				t.Fatalf("电信: %+v", p)
			}
		case cu:
			if p.Sent != 6 || p.Lost != 2 || p.Avg() != 250_000 || p.RTTMax != 300_000 {
				t.Fatalf("联通: %+v", p)
			}
		}
	}
	if all, _ := st.ProbeStats(ctx, 0, LiveMinute, from, to, 60); len(all) != 3 {
		t.Fatalf("every server's minutes: %+v", all)
	}
	if usual, _ := st.ProbeBaselines(ctx, from, 1); usual[ProbeKey{srv.ID, ct}] != 180_000 || usual[ProbeKey{srv.ID, cu}] != 250_000 {
		t.Fatalf("usual round trips: %+v", usual)
	}
	if usual, _ := st.ProbeBaselines(ctx, from, 6); len(usual) != 0 {
		t.Fatalf("an hour is too little to call usual: %+v", usual)
	}

	// The same address keeps its identity and its measurements; a target
	// taken off the list goes with them.
	kept, err := st.ReplaceProbeTargets(ctx, []domain.ProbeTarget{{Name: "四川联通", Host: "cu.example.com", Port: 80, Carrier: "cu", OnCard: true}})
	if err != nil || len(kept) != 1 || kept[0].ID != cu || kept[0].Name != "四川联通" {
		t.Fatalf("kept: %+v %v", kept, err)
	}
	if left, _ := st.ProbeStats(ctx, srv.ID, LiveMinute, from, to, 60); len(left) != 1 || left[0].TargetID != cu {
		t.Fatalf("measurements left: %+v", left)
	}

	// Minutes are kept two days, hours three months.
	now = now.Add(49 * time.Hour)
	if err := st.PruneLive(ctx, 48*time.Hour, 90*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	minutes, _ := st.ServerMetrics(ctx, srv.ID, LiveMinute, from, to, 60)
	hours, _ = st.ServerMetrics(ctx, srv.ID, LiveHour, from, to, 3600)
	if len(minutes) != 0 || len(hours) != 1 {
		t.Fatalf("after two days: minutes %+v hours %+v", minutes, hours)
	}
}
