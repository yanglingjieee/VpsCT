package desired

import (
	"context"
	"path/filepath"
	"testing"

	"ctlvps/internal/domain"
	"ctlvps/internal/provision"
	"ctlvps/internal/store"
)

func TestAServerWithoutInboundsIsOnlyWatched(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "watch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	server := domain.Server{Name: "fixture", Enabled: true, CoreMode: domain.CoreModeStable}
	if err := s.CreateServer(ctx, &server); err != nil {
		t.Fatal(err)
	}
	b := New(s)
	ds, err := b.Build(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	if ds.Tuning.EnableBBR || ds.Tuning.Chrony {
		t.Fatalf("a watched host would be tuned: %+v", ds.Tuning)
	}
	n, err := provision.NewNode(server, "", provision.Options{Protocol: "ss", Port: 21001})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateNode(ctx, &n); err != nil {
		t.Fatal(err)
	}
	if ds, err = b.Build(ctx, server); err != nil {
		t.Fatal(err)
	}
	if !ds.Tuning.EnableBBR || !ds.Tuning.Chrony {
		t.Fatalf("a host that carries traffic is not tuned: %+v", ds.Tuning)
	}
}
