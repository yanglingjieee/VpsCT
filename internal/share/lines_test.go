package share

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ctlvps/internal/agentproto"
	"ctlvps/internal/desired"
	"ctlvps/internal/domain"
	"ctlvps/internal/provision"
	"ctlvps/internal/store"
	"ctlvps/internal/subscription"
	"ctlvps/internal/traffic"
)

type lineFixture struct {
	m                   *Manager
	st                  *store.Store
	now                 *time.Time
	servers             map[string]domain.Server
	nodes               map[string]domain.Node
	direct, relay, hkTo domain.Line
}

func lineSetup(t *testing.T) *lineFixture {
	t.Helper()
	m, st, vmiss, now := setup(t)
	ctx := context.Background()
	f := &lineFixture{m: m, st: st, now: now, servers: map[string]domain.Server{"vmiss": vmiss}, nodes: map[string]domain.Node{}}
	for name, host := range map[string]string{"hk": "8.0.0.1", "att": "99.0.0.1"} {
		s := domain.Server{Name: name + "-server", PublicHost: host, Enabled: true, CoreMode: domain.CoreModeStable}
		if err := st.CreateServer(ctx, &s); err != nil {
			t.Fatal(err)
		}
		f.servers[name] = s
	}
	for name, port := range map[string]int{"vmiss": 443, "hk": 443, "att": 26903} {
		n, err := provision.NewNode(f.servers[name], "", provision.Options{Name: name, Protocol: "vless", Port: port})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.CreateNode(ctx, &n); err != nil {
			t.Fatal(err)
		}
		f.nodes[name] = n
	}
	att := f.nodes["att"].ID
	// The relay sorts first on purpose: menus still follow this order, and
	// its entry must be named after the entry's own direct line.
	f.relay = domain.Line{Name: "ATT via VMISS", EntryNodeID: f.nodes["vmiss"].ID, LandingNodeID: &att, SortOrder: 1, Enabled: true}
	f.direct = domain.Line{Name: "VMISS", EntryNodeID: f.nodes["vmiss"].ID, SortOrder: 2, Enabled: true}
	f.hkTo = domain.Line{Name: "ATT via HK", EntryNodeID: f.nodes["hk"].ID, LandingNodeID: &att, SortOrder: 3, Enabled: true}
	for _, l := range []*domain.Line{&f.relay, &f.direct, &f.hkTo} {
		if err := st.CreateLine(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *lineFixture) members(t *testing.T, sh domain.Share) map[string]domain.Node {
	t.Helper()
	nodes, err := f.st.ListNodes(context.Background(), store.NodeFilter{ShareID: &sh.ID})
	if err != nil {
		t.Fatal(err)
	}
	names := map[int64]string{}
	for name, n := range f.nodes {
		names[n.ID] = name
	}
	out := map[string]domain.Node{}
	for _, n := range nodes {
		if n.AttachNodeID == nil || n.ListenPort != 0 {
			t.Fatalf("a line share owns only members: %+v", n)
		}
		out[names[*n.AttachNodeID]] = n
	}
	return out
}

func (f *lineFixture) desired(t *testing.T, server string) *agentproto.DesiredState {
	t.Helper()
	rec, err := f.st.LatestDesiredState(context.Background(), f.servers[server].ID)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := desired.Load(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := agentproto.ValidateDesired(ds, f.servers[server].ID, 0, ""); err != nil {
		t.Fatalf("%s: agents would refuse this state: %v", server, err)
	}
	return ds
}

func uuidOf(t *testing.T, n domain.Node) string {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(n.ServerParams, &p); err != nil {
		t.Fatal(err)
	}
	id, _ := p["uuid"].(string)
	if len(id) != 36 {
		t.Fatalf("no credential: %s", n.ServerParams)
	}
	return id
}

func TestLineSharesGetTheirOwnCredentials(t *testing.T) {
	f := lineSetup(t)
	ctx := context.Background()
	yang := &domain.Share{Name: "YANG", QuotaBytes: 1000, BillingMode: "sum", ResetDay: 1, LineMode: domain.ShareLinesAll}
	sansan := &domain.Share{Name: "sansan", BillingMode: "sum", ResetDay: 1, LineMode: domain.ShareLinesSelected, LineIDs: []int64{f.direct.ID, f.relay.ID}}
	for _, sh := range []*domain.Share{yang, sansan} {
		if _, err := f.m.Create(ctx, sh); err != nil {
			t.Fatal(err)
		}
	}
	ym, sm := f.members(t, *yang), f.members(t, *sansan)
	if len(ym) != 3 || len(sm) != 2 {
		t.Fatalf("members: yang %d sansan %d", len(ym), len(sm))
	}
	if !ym["att"].Uncounted || ym["vmiss"].Uncounted || ym["hk"].Uncounted {
		t.Fatal("only the landing hop is free of charge")
	}
	seen := map[string]bool{uuidOf(t, domain.Node{ServerParams: f.nodes["vmiss"].ServerParams}): true}
	for _, n := range []domain.Node{ym["vmiss"], ym["hk"], ym["att"], sm["vmiss"], sm["att"]} {
		id := uuidOf(t, n)
		if seen[id] {
			t.Fatal("two users share a credential")
		}
		seen[id] = true
	}

	// Agents: one listener, one extra user per share, nothing else opened.
	ds := f.desired(t, "vmiss")
	if len(ds.Nodes) != 3 {
		t.Fatalf("vmiss: %+v", ds.Nodes)
	}
	for _, n := range ds.Nodes {
		if n.NodeID == f.nodes["vmiss"].ID {
			if n.AttachTo != 0 || n.ListenPort != 443 {
				t.Fatalf("listener changed: %+v", n)
			}
			continue
		}
		if n.AttachTo != f.nodes["vmiss"].ID || n.ListenPort != 0 || n.Blocked || len(n.AllowFrom) != 0 {
			t.Fatalf("entry member: %+v", n)
		}
	}
	sources := func() map[int64][]string {
		out := map[int64][]string{}
		for _, n := range f.desired(t, "att").Nodes {
			if n.AttachTo != 0 {
				out[n.NodeID] = n.AllowFrom
			}
		}
		return out
	}
	got := sources()
	if strings.Join(got[ym["att"].ID], ",") != "1.2.3.4,8.0.0.1" || strings.Join(got[sm["att"].ID], ",") != "1.2.3.4" {
		t.Fatalf("a landing credential is valid only from that user's entries: %v", got)
	}

	// Subscription: lines by name and order, each with the user's own secret.
	svc := subscription.NewService(f.st)
	render := func(sh *domain.Share) (string, *subscription.Bundle) {
		sub, err := f.st.GetSubscriptionByShare(ctx, sh.ID)
		if err != nil {
			t.Fatal(err)
		}
		r, b, err := svc.Render(ctx, sub, subscription.FormatMihomo)
		if err != nil {
			t.Fatal(err)
		}
		return string(r.Body), b
	}
	body, b := render(yang)
	if strings.Join(b.AllProxyNames(), "|") != "ATT via VMISS|VMISS|ATT via HK" {
		t.Fatalf("menu order: %v", b.AllProxyNames())
	}
	if len(b.Proxies) != 2 || len(b.Chains) != 2 || b.Chains[0].Via != "VMISS" || b.Chains[1].Via != "hk" {
		t.Fatalf("bundle: %d proxies, chains %+v", len(b.Proxies), b.Chains)
	}
	for _, want := range []string{uuidOf(t, ym["vmiss"]), uuidOf(t, ym["hk"]), uuidOf(t, ym["att"]), "dialer-proxy: VMISS", "server: 99.0.0.1", "port: 26903"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q", want)
		}
	}
	for _, leak := range []string{uuidOf(t, sm["vmiss"]), uuidOf(t, sm["att"]), uuidOf(t, f.nodes["vmiss"]), uuidOf(t, f.nodes["att"])} {
		if strings.Contains(body, leak) {
			t.Fatal("someone else's credential leaked into the subscription")
		}
	}
	if _, sb := render(sansan); strings.Join(sb.AllProxyNames(), "|") != "ATT via VMISS|VMISS" {
		t.Fatalf("sansan sees only her lines: %v", sb.AllProxyNames())
	}

	// Metering: charged at the entry, from the user's point of view; the
	// landing hop is recorded but free.
	ing := traffic.New(f.st)
	ing.Now = func() time.Time { return *f.now }
	beat := func(server string, epoch string, counters ...agentproto.PortCounter) traffic.Result {
		for i := range counters {
			counters[i].Source, counters[i].Epoch = "nft-node-v1", epoch
		}
		*f.now = f.now.Add(time.Second)
		res, err := ing.Ingest(ctx, f.servers[server], agentproto.Heartbeat{Epoch: epoch, TS: *f.now, Ports: counters})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	beat("vmiss", "v", agentproto.PortCounter{NodeID: ym["vmiss"].ID, Rx: 0, Tx: 0, FromZero: true}, agentproto.PortCounter{NodeID: sm["vmiss"].ID, FromZero: true})
	res := beat("vmiss", "v", agentproto.PortCounter{NodeID: ym["vmiss"].ID, Rx: 700, Tx: 40, FromZero: true}, agentproto.PortCounter{NodeID: sm["vmiss"].ID, Rx: 5, Tx: 1, FromZero: true})
	byShare := map[int64]traffic.ShareDelta{}
	for _, d := range res.Shares {
		byShare[d.ShareID] = d
	}
	if d := byShare[yang.ID]; d.Up != 40 || d.Down != 700 {
		t.Fatalf("bytes received from the destination are the user's download: %+v", d)
	}
	if d := byShare[sansan.ID]; d.Up != 1 || d.Down != 5 {
		t.Fatalf("sansan: %+v", d)
	}
	beat("att", "a", agentproto.PortCounter{NodeID: ym["att"].ID, FromZero: true})
	if res = beat("att", "a", agentproto.PortCounter{NodeID: ym["att"].ID, Rx: 9000, Tx: 9000, FromZero: true}); len(res.Shares) != 0 {
		t.Fatalf("relay traffic charged twice: %+v", res.Shares)
	}
	if up, down, _ := f.st.SumTraffic(ctx, store.SubjectNode, ym["att"].ID, f.now.AddDate(0, 0, -1), f.now.Add(time.Hour)); up+down != 18000 {
		t.Fatalf("landing usage must still be visible per node: %d %d", up, down)
	}

	// Quota: only the user who ran out is cut off, everywhere.
	if err := f.m.EvaluateDeltas(ctx, []traffic.ShareDelta{{ShareID: yang.ID}}); err != nil {
		t.Fatal(err)
	}
	beat("vmiss", "v", agentproto.PortCounter{NodeID: ym["vmiss"].ID, Rx: 2000, Tx: 40, FromZero: true}, agentproto.PortCounter{NodeID: sm["vmiss"].ID, Rx: 5, Tx: 1, FromZero: true})
	if err := f.m.EvaluateDeltas(ctx, []traffic.ShareDelta{{ShareID: yang.ID}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.st.GetShare(ctx, yang.ID); got.Status != domain.ShareExhausted {
		t.Fatalf("yang used %d of %d: %s", got.UsedUpload+got.UsedDownload, got.QuotaBytes, got.Status)
	}
	for _, server := range []string{"vmiss", "hk", "att"} {
		for _, n := range f.desired(t, server).Nodes {
			mine := n.ShareID != nil && *n.ShareID == yang.ID
			if n.Blocked != mine {
				t.Fatalf("%s node %d blocked=%v", server, n.NodeID, n.Blocked)
			}
		}
	}
	if body, b = render(yang); len(b.Proxies)+len(b.Chains) != 0 || !strings.Contains(body, "REJECT") {
		t.Fatalf("an exhausted subscription must stop, not fall back to direct connections:\n%s", body[:min(len(body), 3000)])
	}
	if strings.Contains(body, "proxies: [DIRECT]") || strings.Contains(body, "- DIRECT\n  - name") {
		t.Fatalf("a group was emptied into DIRECT:\n%s", body[:min(len(body), 3000)])
	}

	// Lines changing: credentials follow, and so do the allowed sources.
	f.relay.Enabled = false
	if err := f.st.UpdateLine(ctx, &f.relay); err != nil {
		t.Fatal(err)
	}
	if err := f.m.SyncLines(ctx); err != nil {
		t.Fatal(err)
	}
	if got = sources(); strings.Join(got[ym["att"].ID], ",") != "8.0.0.1" {
		t.Fatalf("sources after a line was disabled: %v", got)
	}
	if _, ok := got[sm["att"].ID]; ok || len(f.members(t, *sansan)) != 1 {
		t.Fatal("sansan's landing credential must be revoked with her only relay line")
	}
	old := uuidOf(t, sm["att"])
	f.relay.Enabled = true
	if err := f.st.UpdateLine(ctx, &f.relay); err != nil {
		t.Fatal(err)
	}
	if err := f.m.SyncLines(ctx); err != nil {
		t.Fatal(err)
	}
	if again := f.members(t, *sansan)["att"]; again.ID != sm["att"].ID || uuidOf(t, again) == old {
		t.Fatal("a returning credential keeps its identity but not its old secret")
	}

	// Rotating the listener's handshake reaches every member without
	// touching anyone's secret.
	parent := f.nodes["vmiss"]
	if err := provision.RegenerateCredentials(&parent, f.servers["vmiss"]); err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpdateNode(ctx, &parent); err != nil {
		t.Fatal(err)
	}
	if err := f.m.SyncLines(ctx); err != nil {
		t.Fatal(err)
	}
	after := f.members(t, *sansan)["vmiss"]
	var mp, pp map[string]any
	_ = json.Unmarshal(after.Params, &mp)
	_ = json.Unmarshal(parent.Params, &pp)
	if uuidOf(t, after) != uuidOf(t, sm["vmiss"]) || mp["reality-opts"].(map[string]any)["public-key"] != pp["reality-opts"].(map[string]any)["public-key"] {
		t.Fatal("member did not follow its listener")
	}

	// Removing the listener removes its lines and credentials.
	if err := f.st.DeleteNode(ctx, f.nodes["att"].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.m.SyncLines(ctx); err != nil {
		t.Fatal(err)
	}
	if lines, _ := f.st.ListLines(ctx); len(lines) != 1 || len(f.members(t, *sansan)) != 1 {
		t.Fatalf("lines %d members %d", len(lines), len(f.members(t, *sansan)))
	}
}
