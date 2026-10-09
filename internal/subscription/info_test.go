package subscription

import (
	"context"
	"ctlvps/internal/domain"
	"ctlvps/internal/proxynode"
	"ctlvps/internal/store"
	"path/filepath"
	"strings"
	"testing"
)

func TestExternalMetadataIsNotAProxy(t *testing.T) {
	ctx := context.Background()
	st, e := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	ext := domain.ExternalSubscription{Name: "upstream", URL: "https://example.test/sub", Enabled: true}
	if e = st.CreateExternal(ctx, &ext); e != nil {
		t.Fatal(e)
	}
	svc := NewService(st)
	real := proxynode.Proxy{Name: "working", Type: "ss", Server: "edge.example.test", Port: 443, Params: map[string]any{"cipher": "aes-128-gcm", "password": "synthetic-test"}}
	info := real
	info.Name = "⌛剩余流量 123.45GB"
	res := &FetchResult{Proxies: []proxynode.Proxy{real, info}}
	stats, e := svc.applyExternal(ctx, ext, res)
	if e != nil || stats.Total != 1 {
		t.Fatalf("metadata imported: total=%d err=%v", stats.Total, e)
	}
	if len(res.Proxies) != 2 {
		t.Fatal("caller fetch result mutated")
	}
	if _, e = svc.applyExternal(ctx, ext, &FetchResult{Proxies: []proxynode.Proxy{info}}); e == nil {
		t.Fatal("metadata-only response accepted")
	}
	nodes, e := st.ListNodes(ctx, store.NodeFilter{ExternalSubID: &ext.ID})
	if e != nil || len(nodes) != 1 {
		t.Fatal("metadata-only update erased working nodes", e)
	}
	// Rows imported before the fix must be excluded without deleting user data.
	legacy := info.ToDomain()
	legacy.Source = domain.NodeImported
	legacy.ExternalSubID = &ext.ID
	if e = st.CreateNode(ctx, &legacy); e != nil {
		t.Fatal(e)
	}
	sh := domain.Share{Name: "share", Status: domain.ShareActive, ExtraNodeIDs: []int64{legacy.ID, nodes[0].ID}}
	if e = st.CreateShare(ctx, &sh); e != nil {
		t.Fatal(e)
	}
	b, e := svc.Build(ctx, domain.Subscription{Kind: domain.SubShare, ShareID: &sh.ID})
	if e != nil {
		t.Fatal(e)
	}
	if len(b.Proxies) != 1 {
		t.Fatalf("a user's profile includes metadata: %d proxies", len(b.Proxies))
	}
	for _, kind := range ProfileKinds {
		r, e := RenderProfile(b, kind, "MATCH,PROXY\n", "", "https://panel.example.test")
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(r.Body), info.Name) {
			t.Fatalf("metadata in %s", kind)
		}
	}
	if _, e = st.GetNode(ctx, legacy.ID); e != nil {
		t.Fatal("legacy user row deleted")
	}
}
