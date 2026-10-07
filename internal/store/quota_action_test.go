package store

import (
	"context"
	"path/filepath"
	"testing"

	"ctlvps/internal/domain"
)

func TestPanelWideQuotaStopBecomesEachServersChoice(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "quota.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	metered := domain.Server{Name: "metered", Enabled: true, QuotaBytes: 100}
	unmetered := domain.Server{Name: "unmetered", Enabled: true}
	for _, v := range []*domain.Server{&metered, &unmetered} {
		if err := s.CreateServer(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetSetting(ctx, "quota.action", "disable"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.GetServer(ctx, metered.ID)
	b, _ := s.GetServer(ctx, unmetered.ID)
	if !a.QuotaStop || b.QuotaStop || s.GetSetting(ctx, "quota.action", "gone") != "gone" {
		t.Fatalf("metered=%v unmetered=%v setting=%q", a.QuotaStop, b.QuotaStop, s.GetSetting(ctx, "quota.action", "gone"))
	}
}
