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
		s := domain.Server{Name: name + "-server", PublicHost: host, Enabled: true, CoreMode: domain.CoreModeStable, StrictSource: name == "att"}
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
	lines, _ := f.st.ListLines(context.Background())
	names := map[int64]string{}
	for _, l := range lines {
		names[l.ID] = l.Name
	}
	out := map[string]domain.Node{}
	for _, n := range nodes {
		if n.AttachNodeID == nil || n.LineID == nil || n.ListenPort != 0 {
			t.Fatalf("a line share owns only members: %+v", n)
		}
		key := names[*n.LineID]
		if n.Landing {
			key += "/landing"
		}
		out[key] = n
	}
	return out
}

func (f *lineFixture) desired(t *testing.T, server string) map[int64]agentproto.NodeSpec {
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
	out := map[int64]agentproto.NodeSpec{}
	for _, n := range ds.Nodes {
		out[n.NodeID] = n
	}
	return out
}

func param(t *testing.T, raw []byte, key string) string {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	v, _ := p[key].(string)
	return v
}

func uuidOf(t *testing.T, n domain.Node) string {
	t.Helper()
	id := param(t, n.ServerParams, "uuid")
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
	if len(ym) != 5 || len(sm) != 3 {
		t.Fatalf("one credential per line and machine: yang %d sansan %d", len(ym), len(sm))
	}
	seen := map[string]bool{uuidOf(t, f.nodes["vmiss"]): true, uuidOf(t, f.nodes["att"]): true}
	for _, set := range []map[string]domain.Node{ym, sm} {
		for name, n := range set {
			id := uuidOf(t, n)
			if seen[id] {
				t.Fatalf("%s shares a credential", name)
			}
			seen[id] = true
		}
	}

	// The entry server relays: the user's credential there carries their own
	// credential on the landing. Nothing else is opened.
	vmiss, att := f.desired(t, "vmiss"), f.desired(t, "att")
	if len(vmiss) != 5 || len(att) != 4 {
		t.Fatalf("vmiss %d att %d", len(vmiss), len(att))
	}
	relay := vmiss[ym["ATT via VMISS"].ID]
	if relay.AttachTo != f.nodes["vmiss"].ID || relay.ListenPort != 0 || relay.Blocked || relay.Relay == nil {
		t.Fatalf("relay member: %+v", relay)
	}
	if r := relay.Relay; r.Server != "99.0.0.1" || r.Port != 26903 || r.UUID != uuidOf(t, ym["ATT via VMISS/landing"]) ||
		r.PublicKey != param(t, f.nodes["att"].ServerParams, "reality_public_key") || r.ServerName != param(t, f.nodes["att"].ServerParams, "handshake_server") {
		t.Fatalf("relay target: %+v", r)
	}
	if d := vmiss[ym["VMISS"].ID]; d.Relay != nil || d.AttachTo != f.nodes["vmiss"].ID || len(d.AllowFrom) != 0 {
		t.Fatalf("direct member: %+v", d)
	}
	if got := att[ym["ATT via VMISS/landing"].ID].AllowFrom; len(got) != 1 || got[0] != "1.2.3.4" {
		t.Fatalf("a landing credential is valid only from its line's entry: %v", got)
	}
	if got := att[ym["ATT via HK/landing"].ID].AllowFrom; len(got) != 1 || got[0] != "8.0.0.1" {
		t.Fatalf("landing source: %v", got)
	}

	// Subscription: every line is one ordinary proxy on its entry. The
	// landing's address and credentials never reach a client.
	svc := subscription.NewService(f.st)
	render := func(sh *domain.Share, format string) (string, *subscription.Bundle) {
		sub, err := f.st.GetSubscriptionByShare(ctx, sh.ID)
		if err != nil {
			t.Fatal(err)
		}
		r, b, err := svc.Render(ctx, sub, format, "https://panel.example.test")
		if err != nil {
			t.Fatal(err)
		}
		return string(r.Body), b
	}
	body, b := render(yang, subscription.FormatMihomo)
	if strings.Join(b.AllProxyNames(), "|") != "ATT via VMISS|VMISS|ATT via HK" || len(b.Chains) != 0 {
		t.Fatalf("menu order: %v, chains %d", b.AllProxyNames(), len(b.Chains))
	}
	for _, want := range []string{uuidOf(t, ym["ATT via VMISS"]), uuidOf(t, ym["VMISS"]), uuidOf(t, ym["ATT via HK"]), "server: 1.2.3.4", "server: 8.0.0.1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q", want)
		}
	}
	for _, leak := range []string{"99.0.0.1", "26903", uuidOf(t, ym["ATT via VMISS/landing"]), uuidOf(t, sm["VMISS"]), uuidOf(t, f.nodes["vmiss"]), "dialer-proxy"} {
		if strings.Contains(body, leak) {
			t.Fatalf("%q must not be in a client profile", leak)
		}
	}
	if _, sb := render(sansan, subscription.FormatMihomo); strings.Join(sb.AllProxyNames(), "|") != "ATT via VMISS|VMISS" {
		t.Fatalf("sansan sees only her lines: %v", sb.AllProxyNames())
	}
	if uris, _ := render(yang, subscription.FormatURIList); strings.Count(uris, "vless://") != 3 {
		t.Fatalf("every line, relays included, is a plain node link:\n%s", uris)
	}

	// Metering: per user on both machines of a relay, from the user's point
	// of view, and both count.
	ing := traffic.New(f.st)
	ing.Now = func() time.Time { return *f.now }
	beat := func(server string, epoch string, counters ...agentproto.PortCounter) traffic.Result {
		for i := range counters {
			counters[i].Source, counters[i].Epoch, counters[i].FromZero = "nft-node-v1", epoch, true
		}
		*f.now = f.now.Add(time.Second)
		res, err := ing.Ingest(ctx, f.servers[server], agentproto.Heartbeat{Epoch: epoch, TS: *f.now, Ports: counters})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	usage := func(res traffic.Result, sh *domain.Share) (up, down int64) {
		for _, d := range res.Shares {
			if d.ShareID == sh.ID {
				return d.Up, d.Down
			}
		}
		return 0, 0
	}
	yr, sr, yl := ym["ATT via VMISS"].ID, sm["VMISS"].ID, ym["ATT via VMISS/landing"].ID
	beat("vmiss", "v", agentproto.PortCounter{NodeID: yr}, agentproto.PortCounter{NodeID: sr})
	res := beat("vmiss", "v", agentproto.PortCounter{NodeID: yr, Rx: 300, Tx: 20}, agentproto.PortCounter{NodeID: sr, Rx: 5, Tx: 1})
	if up, down := usage(res, yang); up != 20 || down != 300 {
		t.Fatalf("bytes received from the far side are the user's download: %d %d", up, down)
	}
	if up, down := usage(res, sansan); up != 1 || down != 5 {
		t.Fatalf("sansan: %d %d", up, down)
	}
	beat("att", "a", agentproto.PortCounter{NodeID: yl})
	res = beat("att", "a", agentproto.PortCounter{NodeID: yl, Rx: 290, Tx: 18})
	if up, down := usage(res, yang); up != 18 || down != 290 {
		t.Fatalf("the landing machine carried the traffic too: %d %d", up, down)
	}
	for id, want := range map[int64]int64{yr: 320, yl: 308} {
		if up, down, _ := f.st.SumTraffic(ctx, store.SubjectNode, id, f.now.AddDate(0, 0, -1), f.now.Add(time.Hour)); up+down != want {
			t.Fatalf("per-machine usage of node %d: %d", id, up+down)
		}
	}
	if got, _ := f.st.GetShare(ctx, yang.ID); got.UsedUpload+got.UsedDownload != 628 || got.Status != domain.ShareActive {
		t.Fatalf("yang: %+v", got)
	}

	// Quota: only the user who ran out is cut off, on every machine.
	res = beat("vmiss", "v", agentproto.PortCounter{NodeID: yr, Rx: 900, Tx: 20}, agentproto.PortCounter{NodeID: sr, Rx: 5, Tx: 1})
	if err := f.m.EvaluateDeltas(ctx, res.Shares); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.st.GetShare(ctx, yang.ID); got.Status != domain.ShareExhausted {
		t.Fatalf("yang used %d of %d: %s", got.UsedUpload+got.UsedDownload, got.QuotaBytes, got.Status)
	}
	for _, server := range []string{"vmiss", "hk", "att"} {
		for _, n := range f.desired(t, server) {
			if mine := n.ShareID != nil && *n.ShareID == yang.ID; n.Blocked != mine {
				t.Fatalf("%s node %d blocked=%v", server, n.NodeID, n.Blocked)
			}
		}
	}
	if body, b = render(yang, subscription.FormatMihomo); len(b.Proxies) != 0 || !strings.Contains(body, "REJECT") {
		t.Fatalf("an exhausted profile must stop, not fall back to direct connections:\n%s", body[:min(len(body), 2000)])
	}
	if err := f.m.ResetUsage(ctx, yang.ID); err != nil {
		t.Fatal(err)
	}

	// Lines changing: credentials follow on both machines.
	f.relay.Enabled = false
	if err := f.st.UpdateLine(ctx, &f.relay); err != nil {
		t.Fatal(err)
	}
	if err := f.m.SyncLines(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.members(t, *sansan)) != 1 || len(f.desired(t, "att")) != 2 || len(f.desired(t, "vmiss")) != 3 {
		t.Fatalf("a disabled relay leaves nothing behind: %d members", len(f.members(t, *sansan)))
	}
	old := uuidOf(t, sm["ATT via VMISS"])
	f.relay.Enabled = true
	if err := f.st.UpdateLine(ctx, &f.relay); err != nil {
		t.Fatal(err)
	}
	if err := f.m.SyncLines(ctx); err != nil {
		t.Fatal(err)
	}
	if again := f.members(t, *sansan)["ATT via VMISS"]; again.ID != sm["ATT via VMISS"].ID || uuidOf(t, again) == old {
		t.Fatal("a returning credential keeps its identity but not its old secret")
	}

	// Moving a line to another entry moves the credential.
	f.relay.EntryNodeID = f.nodes["hk"].ID
	if err := f.st.UpdateLine(ctx, &f.relay); err != nil {
		t.Fatal(err)
	}
	if err := f.m.SyncLines(ctx); err != nil {
		t.Fatal(err)
	}
	moved := f.members(t, *sansan)["ATT via VMISS"]
	if *moved.AttachNodeID != f.nodes["hk"].ID || f.desired(t, "hk")[moved.ID].Relay == nil || len(f.desired(t, "vmiss")) != 3 {
		t.Fatalf("line moved to hk: %+v", moved)
	}
	if got := f.desired(t, "att")[f.members(t, *sansan)["ATT via VMISS/landing"].ID].AllowFrom; len(got) != 1 || got[0] != "8.0.0.1" {
		t.Fatalf("the landing follows its new entry: %v", got)
	}

	// Rotating a landing's handshake reaches the entry servers that relay
	// to it, without touching anyone's secret.
	parent := f.nodes["att"]
	if err := provision.RegenerateCredentials(&parent, f.servers["att"]); err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpdateNode(ctx, &parent); err != nil {
		t.Fatal(err)
	}
	if err := f.m.SyncLines(ctx); err != nil {
		t.Fatal(err)
	}
	now := f.members(t, *yang)
	if r := f.desired(t, "hk")[now["ATT via HK"].ID].Relay; r == nil || r.PublicKey != param(t, parent.ServerParams, "reality_public_key") || r.UUID != uuidOf(t, ym["ATT via HK/landing"]) {
		t.Fatalf("entry did not follow its landing: %+v", r)
	}

	// A landing behind address translation never sees the entry's address:
	// the check is the landing server's choice.
	nat := f.servers["att"]
	nat.StrictSource = false
	if err := f.st.UpdateServer(ctx, &nat); err != nil {
		t.Fatal(err)
	}
	if err := f.m.SyncLines(ctx); err != nil {
		t.Fatal(err)
	}
	for _, n := range f.desired(t, "att") {
		if len(n.AllowFrom) != 0 {
			t.Fatalf("source check must follow the landing server: %+v", n)
		}
	}

	// Removing the landing listener removes its lines and every credential
	// of them, on both machines.
	if err := f.st.DeleteNode(ctx, f.nodes["att"].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.m.SyncLines(ctx); err != nil {
		t.Fatal(err)
	}
	if lines, _ := f.st.ListLines(ctx); len(lines) != 1 || len(f.members(t, *sansan)) != 1 || len(f.desired(t, "hk")) != 1 {
		t.Fatalf("lines %d members %d", len(lines), len(f.members(t, *sansan)))
	}
}

func TestLinesOnOtherProtocols(t *testing.T) {
	m, st, entrySrv, _ := setup(t)
	ctx := context.Background()
	landingSrv := domain.Server{Name: "landing-server", PublicHost: "99.0.0.9", Enabled: true, CoreMode: domain.CoreModeStable}
	if err := st.CreateServer(ctx, &landingSrv); err != nil {
		t.Fatal(err)
	}
	deploy := func(srv domain.Server, proto string, port int) domain.Node {
		n, err := provision.NewNode(srv, "", provision.Options{Name: proto, Protocol: proto, Port: port})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.CreateNode(ctx, &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	trojan, hy2 := deploy(entrySrv, "trojan", 443), deploy(entrySrv, "hysteria2", 8443)
	ss, snell, tuic := deploy(landingSrv, "ss", 26903), deploy(entrySrv, "snell", 9443), deploy(landingSrv, "tuic", 26904)
	line := func(name string, entry domain.Node, landing *domain.Node) error {
		l := domain.Line{Name: name, EntryNodeID: entry.ID, Enabled: true}
		if landing != nil {
			l.LandingNodeID = &landing.ID
		}
		return st.CreateLine(ctx, &l)
	}
	if err := line("snell", snell, nil); err == nil {
		t.Fatal("a single-identity protocol cannot be shared by users")
	}
	if err := line("tls landing", trojan, &tuic); err == nil {
		t.Fatal("a landing that needs a certificate the relay cannot verify was accepted")
	}
	for name, err := range map[string]error{"direct hy2": line("QUIC", hy2, nil), "relay": line("Relay", trojan, &ss)} {
		if err != nil {
			t.Fatal(name, err)
		}
	}
	a := &domain.Share{Name: "a", LineMode: domain.ShareLinesAll}
	b := &domain.Share{Name: "b", LineMode: domain.ShareLinesAll}
	for _, sh := range []*domain.Share{a, b} {
		if _, err := m.Create(ctx, sh); err != nil {
			t.Fatal(err)
		}
	}
	load := func(server domain.Server) map[int64]agentproto.NodeSpec {
		rec, err := st.LatestDesiredState(ctx, server.ID)
		if err != nil {
			t.Fatal(err)
		}
		ds, _ := desired.Load(rec)
		if err := agentproto.ValidateDesired(ds, server.ID, 0, ""); err != nil {
			t.Fatalf("agents would refuse this state: %v", err)
		}
		out := map[int64]agentproto.NodeSpec{}
		for _, n := range ds.Nodes {
			out[n.NodeID] = n
		}
		return out
	}
	entry, landing := load(entrySrv), load(landingSrv)
	relays, secrets := 0, map[string]bool{}
	for _, n := range entry {
		if n.AttachTo == 0 {
			continue
		}
		pw, _ := n.Params["password"].(string)
		if pw == "" || secrets[pw] {
			t.Fatalf("every user needs their own secret on %s: %+v", n.Protocol, n.Params)
		}
		secrets[pw] = true
		if n.AttachTo == trojan.ID {
			relays++
			r := n.Relay
			serverKey := param(t, ss.ServerParams, "password")
			if r == nil || r.Protocol != "ss" || r.Server != "99.0.0.9" || r.Port != 26903 || !strings.HasPrefix(r.Password, serverKey+":") || r.UDPOverTCP {
				t.Fatalf("shadowsocks relay target, UDP as UDP unless the landing says otherwise: %+v", r)
			}
			userKey := strings.TrimPrefix(r.Password, serverKey+":")
			found := false
			for _, l := range landing {
				if l.AttachTo == ss.ID && l.Params["password"] == userKey {
					found = true
				}
			}
			if !found {
				t.Fatal("the relay must use this user's own key on the landing")
			}
		}
	}
	if relays != 2 || len(entry) != 3+4 || len(landing) != 2+2 {
		t.Fatalf("entry %d landing %d relays %d", len(entry), len(landing), relays)
	}
	// A landing whose forwarded UDP port is unreliable takes UDP inside TCP.
	landingSrv.UDPOverTCP = true
	if err := st.UpdateServer(ctx, &landingSrv); err != nil {
		t.Fatal(err)
	}
	if err := m.SyncLines(ctx); err != nil {
		t.Fatal(err)
	}
	for _, n := range load(entrySrv) {
		if n.AttachTo == trojan.ID && (n.Relay == nil || !n.Relay.UDPOverTCP || n.Relay.Validate() != nil) {
			t.Fatalf("relay to a UDP-over-TCP landing: %+v", n.Relay)
		}
	}
	sub, _ := st.GetSubscriptionByShare(ctx, a.ID)
	r, bundle, err := subscription.NewService(st).Render(ctx, sub, subscription.FormatMihomo, "https://panel.example.test")
	if err != nil {
		t.Fatal(err)
	}
	body := string(r.Body)
	if strings.Join(bundle.AllProxyNames(), "|") != "QUIC|Relay" || !strings.Contains(body, "type: hysteria2") || !strings.Contains(body, "type: trojan") || strings.Contains(body, "99.0.0.9") {
		t.Fatalf("profile: %v\n%s", bundle.AllProxyNames(), body[:min(len(body), 1500)])
	}
	uris, _, err := subscription.NewService(st).Render(ctx, sub, subscription.FormatURIList, "https://panel.example.test")
	if err != nil || !strings.Contains(string(uris.Body), "hysteria2://") || !strings.Contains(string(uris.Body), "trojan://") {
		t.Fatalf("node links: %v\n%s", err, uris.Body)
	}
}
