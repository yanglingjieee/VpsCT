//go:build linux

package main

import (
	"context"
	"ctlvps/internal/desired"
	"ctlvps/internal/domain"
	"ctlvps/internal/networkconfig"
	"ctlvps/internal/proxynode"
	"ctlvps/internal/share"
	"ctlvps/internal/store"
	"ctlvps/internal/subscription"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
)

// Qualify actual public subscription output against real agent-created inbounds.
// Synthetic credentials and a disposable controller keep production untouched.
func testSubscriptionClients(ctx context.Context, st *store.Store, d *desired.Builder, shares *share.Manager, server domain.Server, admin *fixtureAdmin, apiServer *httptest.Server) {
	run("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=www.sony.com", "-addext", "subjectAltName=DNS:www.sony.com,DNS:reality.fixture", "-keyout", "/tmp/sub-reality.key", "-out", "/tmp/sub-reality.crt")
	stopTLS := process(ctx, "ip", "netns", "exec", "landing", "openssl", "s_server", "-accept", "203.0.113.10:443", "-key", "/tmp/sub-reality.key", "-cert", "/tmp/sub-reality.crt", "-tls1_3", "-alpn", "h2", "-www")
	defer stopTLS()
	protocols := []string{"ss", "vless", "trojan", "anytls", "hysteria2", "tuic", "wireguard"}
	ids := []int64{}
	for i, p := range protocols {
		var n domain.Node
		admin.request("POST", fmt.Sprintf("/api/v1/servers/%d/nodes", server.ID), map[string]any{"protocol": p, "name": "ordinary-" + p, "port": 25400 + i, "sni": "reality.fixture"}, 201, &n)
		ids = append(ids, n.ID)
	}
	sh := domain.Share{Name: "all protocols share", Targets: []domain.ShareTarget{{ServerID: server.ID, Protocols: protocols}}}
	shareToken, e := shares.Create(ctx, &sh)
	must(e)
	// Reality handshakes must resolve through the offline fixture DNS.
	profile := domain.EgressProfile{ServerID: server.ID, Name: "subscription fixture DNS", Kind: "direct", Enabled: true}
	raw, _ := json.Marshal(networkconfig.Direct{Family: "ipv4", DNS: networkconfig.Resolver{Transport: "tcp", Address: "203.0.113.10", Port: 15353}})
	must(st.CreateEgressProfile(ctx, &profile, raw))
	allNodes, e := st.ListNodes(ctx, store.NodeFilter{})
	must(e)
	for _, node := range allNodes {
		if node.Protocol == "vless" {
			_, e = st.SetNodeNetwork(ctx, node.ID, 0, &networkconfig.Node{ListenMode: "all", AdvertiseMode: "inherit", OnUnavailable: "block", EgressProfileID: profile.ID, EgressRevision: 1}, nil)
			must(e)
		}
	}
	_, _, e = d.Publish(ctx, server.ID)
	must(e)
	rec, e := st.LatestDesiredState(ctx, server.ID)
	must(e)
	eventually("all inbounds applied", func() bool {
		a, e := st.GetAgentByServer(ctx, server.ID)
		return e == nil && a.ApplyError == "" && a.AppliedRevision == rec.Revision
	})
	// The official sing-box client is given each inbound as a user's profile
	// would carry it.
	for i, id := range ids {
		n, e := st.GetNode(ctx, id)
		must(e)
		protocol := protocols[i]
		p := proxynode.FromDomain(n)
		doc := map[string]any{
			"log":      map[string]any{"level": "error"},
			"inbounds": []any{map[string]any{"type": "socks", "listen": "127.0.0.1", "listen_port": 1080}},
			"route":    map[string]any{"final": p.Name},
		}
		direct := map[string]any{"type": "direct", "tag": "direct"}
		if ep, ok := subscription.SingBoxEndpoint(p, ""); ok {
			doc["endpoints"], doc["outbounds"] = []any{ep}, []any{direct}
		} else if out, ok := subscription.SingBoxOutbound(p, ""); ok {
			doc["outbounds"] = []any{out, direct}
		} else {
			panic("no sing-box client for " + protocol)
		}
		path := "/tmp/subscription-runtime.json"
		writeJSON(path, doc)
		run("/opt/ctlvps/bin/sing-box", "check", "-c", path)
		stop := process(ctx, "ip", "netns", "exec", "landing", "/opt/ctlvps/bin/sing-box", "run", "-c", path)
		eventually(protocol+" official client", func() bool { got, e := probe(0, "tcp", "203.0.113.10"); return e == nil && got == "192.0.2.1" })
		for _, transport := range []string{"tcp", "udp", "greeting"} {
			got, e := probe(0, transport, "203.0.113.10")
			if e != nil || got != "192.0.2.1" {
				stop()
				panic(fmt.Sprintf("official client %s %s failed", protocol, transport))
			}
		}
		stop()
		fmt.Println("PASS inbound -> official client ->", protocol, "TCP/UDP/server-first")
	}
	must(shares.Pause(ctx, sh.ID))
	response, e := apiServer.Client().Get(apiServer.URL + "/s/" + shareToken + "/uri")
	must(e)
	body, e := io.ReadAll(response.Body)
	response.Body.Close()
	must(e)
	if response.StatusCode != 200 || strings.Contains(string(body), "://") {
		panic("paused share leaked usable proxy")
	}
	_ = os.Remove("/tmp/subscription-runtime.json")
	fmt.Println("PASS paused share excludes dedicated nodes")
}
