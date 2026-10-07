package traffic_test

import (
	"context"
	"ctlvps/internal/agentproto"
	"ctlvps/internal/desired"
	"ctlvps/internal/domain"
	"ctlvps/internal/share"
	"ctlvps/internal/store"
	"ctlvps/internal/traffic"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type serverFixture struct {
	t      *testing.T
	st     *store.Store
	ing    *traffic.Ingestor
	now    time.Time
	rx, tx int64
}

func newServerFixture(t *testing.T, now time.Time) *serverFixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &serverFixture{t: t, st: st, ing: traffic.New(st), now: now}
	st.Now = func() time.Time { return f.now }
	f.ing.Now = st.Now
	return f
}

func (f *serverFixture) server(resetDay int, billing string) domain.Server {
	f.t.Helper()
	srv := domain.Server{Name: "fixture", Enabled: true, CoreMode: domain.CoreModeStable, QuotaResetDay: resetDay, QuotaBilling: billing, QuotaBytes: 1000}
	if err := f.st.CreateServer(context.Background(), &srv); err != nil {
		f.t.Fatal(err)
	}
	return srv
}

// carry reports that the NIC has moved rx/tx more bytes by time at.
func (f *serverFixture) carry(srv domain.Server, at time.Time, rx, tx int64) {
	f.t.Helper()
	f.now = at
	f.rx, f.tx = f.rx+rx, f.tx+tx
	hb := agentproto.Heartbeat{TS: at, Epoch: "boot", Metrics: agentproto.Metrics{NetRx: f.rx, NetTx: f.tx}}
	if _, err := f.ing.Ingest(context.Background(), srv, hb); err != nil {
		f.t.Fatal(err)
	}
}

func (f *serverFixture) usage(srv domain.Server) traffic.ServerUsage {
	f.t.Helper()
	u, err := f.ing.ServerUsage(context.Background(), srv)
	if err != nil {
		f.t.Fatal(err)
	}
	return u
}

func TestServerUsageCountsTheRunningPeriod(t *testing.T) {
	day := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	f := newServerFixture(t, day)
	srv := f.server(1, domain.BillingOut)
	f.carry(srv, day, 1, 1) // baseline
	f.carry(srv, day.Add(time.Minute), 300, 50)
	u := f.usage(srv)
	if u.Inbound != 300 || u.Outbound != 50 || u.Measured != 50 || u.Billed != 50 || u.Percent != 5 {
		t.Fatalf("outbound-only usage = %+v", u)
	}
	if !u.PeriodStart.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || u.NextReset == nil || !u.NextReset.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("period = %v → %v", u.PeriodStart, u.NextReset)
	}
	srv.QuotaBilling = domain.BillingDual
	if u = f.usage(srv); u.Billed != 350 {
		t.Fatalf("both directions = %d", u.Billed)
	}
}

func TestServerPeriodStartsAtMidnightInTheResetTimezone(t *testing.T) {
	ctx := context.Background()
	// 23:00 on 30 September in Shanghai.
	f := newServerFixture(t, time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC))
	if err := f.st.SetSetting(ctx, domain.SettingQuotaTimezone, "Asia/Shanghai"); err != nil {
		t.Fatal(err)
	}
	srv := f.server(1, domain.BillingDual)
	f.carry(srv, f.now, 1, 1)
	f.carry(srv, f.now.Add(30*time.Minute), 100, 100)
	if u := f.usage(srv); u.Billed != 200 || !u.PeriodStart.Equal(time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("September = %+v", u)
	}
	// 00:30 on 1 October in Shanghai, still 30 September in UTC.
	october := time.Date(2026, 9, 30, 16, 30, 0, 0, time.UTC)
	f.now = october
	if u := f.usage(srv); u.Billed != 0 || !u.PeriodStart.Equal(time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("October before its first heartbeat = %+v", u)
	}
	f.carry(srv, october, 7, 3)
	if u := f.usage(srv); u.Billed != 10 {
		t.Fatalf("October = %+v", u)
	}
}

func TestServerUsageCorrection(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	f := newServerFixture(t, day)
	srv := f.server(28, domain.BillingDual)
	f.carry(srv, day, 1, 1)
	f.carry(srv, day.Add(time.Minute), 40, 60)
	used := int64(5000)
	if err := f.ing.CalibrateServer(ctx, srv, &used); err != nil {
		t.Fatal(err)
	}
	if u := f.usage(srv); u.Billed != 5000 || u.Measured != 100 || u.Adjust != 4900 || !u.OverQuota {
		t.Fatalf("corrected = %+v", u)
	}
	f.carry(srv, day.Add(2*time.Minute), 10, 10)
	if u := f.usage(srv); u.Billed != 5020 {
		t.Fatalf("counting on from the correction = %+v", u)
	}
	if err := f.ing.CalibrateServer(ctx, srv, nil); err != nil {
		t.Fatal(err)
	}
	if u := f.usage(srv); u.Billed != 120 || u.Adjust != 0 {
		t.Fatalf("correction removed = %+v", u)
	}
	if err := f.ing.CalibrateServer(ctx, srv, &used); err != nil {
		t.Fatal(err)
	}
	// The correction belongs to its period.
	f.carry(srv, time.Date(2026, 10, 28, 0, 5, 0, 0, time.UTC), 2, 3)
	if u := f.usage(srv); u.Billed != 5 || u.Adjust != 0 {
		t.Fatalf("next period = %+v", u)
	}
	rolling := srv
	rolling.QuotaResetDay = 0
	if err := f.ing.CalibrateServer(ctx, rolling, &used); !errors.Is(err, traffic.ErrNoPeriod) {
		t.Fatalf("rolling window accepted a correction: %v", err)
	}
}

func TestServerPeriodIsSeededFromHistory(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	f := newServerFixture(t, day)
	srv := f.server(1, domain.BillingDual)
	f.carry(srv, time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), 1, 1)
	f.carry(srv, time.Date(2026, 9, 29, 13, 0, 0, 0, time.UTC), 1000, 0)
	f.carry(srv, day, 30, 0)
	if u := f.usage(srv); u.Billed != 30 {
		t.Fatalf("October = %+v", u)
	}
	// Moving the reset day back to the 28th takes the 29th into the period.
	srv.QuotaResetDay = 28
	if err := f.st.DropServerPeriod(ctx, srv.ID); err != nil {
		t.Fatal(err)
	}
	if u := f.usage(srv); u.Billed != 1030 {
		t.Fatalf("read before the next heartbeat = %+v", u)
	}
	f.carry(srv, day.Add(time.Minute), 5, 0)
	if u := f.usage(srv); u.Billed != 1035 {
		t.Fatalf("seeded = %+v", u)
	}
}

func TestChangingTheResetTimezoneKeepsRunningPeriods(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	f := newServerFixture(t, day)
	if err := f.st.SetSetting(ctx, domain.SettingQuotaTimezone, "Asia/Shanghai"); err != nil {
		t.Fatal(err)
	}
	srv := f.server(1, domain.BillingDual)
	f.carry(srv, day, 1, 1)
	f.carry(srv, day.Add(time.Minute), 500, 500)
	manager := share.New(f.st, desired.New(f.st))
	manager.Now = f.st.Now
	sh := domain.Share{Name: "fixture", ResetDay: 1}
	if _, err := manager.Create(ctx, &sh); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.DB().ExecContext(ctx, "UPDATE shares SET used_download=77 WHERE id=?", sh.ID); err != nil {
		t.Fatal(err)
	}
	// Back to UTC the period starts eight hours later; it is the same period.
	if err := f.st.SetSetting(ctx, domain.SettingQuotaTimezone, "UTC"); err != nil {
		t.Fatal(err)
	}
	if err := f.ing.RebasePeriods(ctx); err != nil {
		t.Fatal(err)
	}
	f.carry(srv, day.Add(2*time.Minute), 1, 0)
	if u := f.usage(srv); u.Billed != 1001 || !u.PeriodStart.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("server after the change = %+v", u)
	}
	if err := manager.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := f.st.GetShare(ctx, sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.UsedDownload != 77 || !got.PeriodStart.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("user after the change: used %d since %v", got.UsedDownload, got.PeriodStart)
	}
}
