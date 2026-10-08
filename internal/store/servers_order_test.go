package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"ctlvps/internal/domain"
)

func serverNames(t *testing.T, s *Store) string {
	t.Helper()
	list, err := s.ListServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(list))
	for i, v := range list {
		names[i] = v.Name
	}
	return strings.Join(names, " ")
}

func TestServersKeepTheOperatorsOrder(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	ids := map[string]int64{}
	add := func(name string) {
		v := &domain.Server{Name: name, Enabled: true}
		if err := s.CreateServer(ctx, v); err != nil {
			t.Fatal(err)
		}
		ids[name] = v.ID
	}
	for _, name := range []string{"zeta", "alpha", "mid"} {
		add(name)
	}
	if got := serverNames(t, s); got != "zeta alpha mid" {
		t.Fatalf("a new server goes last, not by name: %q", got)
	}
	// The named ones come first; an unknown or repeated ID changes nothing,
	// and the servers left out keep their order.
	if err := s.ReorderServers(ctx, []int64{ids["mid"], 999, ids["mid"]}); err != nil {
		t.Fatal(err)
	}
	if got := serverNames(t, s); got != "mid zeta alpha" {
		t.Fatalf("after reorder: %q", got)
	}
	v, err := s.GetServer(ctx, ids["alpha"])
	if err != nil {
		t.Fatal(err)
	}
	v.Name = "aaa"
	if err := s.UpdateServer(ctx, &v); err != nil {
		t.Fatal(err)
	}
	add("beta")
	if got := serverNames(t, s); got != "mid zeta aaa beta" {
		t.Fatalf("saving a server must not move it, and a new one goes last: %q", got)
	}
}

func TestServerOrderStartsAsItWasShown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := openAtSchema(path, 36)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO servers(id,name,created_at,updated_at) VALUES
 (1,'b-watched','2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
 (2,'d-lines','2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
 (3,'a-watched','2026-10-01T00:00:00Z','2026-10-01T00:00:00Z'),
 (4,'c-lines','2026-10-01T00:00:00Z','2026-10-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{2, 4} {
		sid := id
		n := &domain.Node{Name: "in", Protocol: "vless", Server: "192.0.2.1", Port: 443, Params: []byte(`{}`), ServerID: &sid, ListenPort: 443, Core: domain.CoreSingBox, Enabled: true, Source: domain.NodeDeployed}
		n.Name += string(rune('0' + id))
		if err := s.CreateNode(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Servers carrying nodes first, then the ones only watched, by name within each.
	if got := serverNames(t, s); got != "c-lines d-lines a-watched b-watched" {
		t.Fatalf("order after migrating: %q", got)
	}
}
