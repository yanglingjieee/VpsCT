package agentlive

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"ctlvps/internal/liveproto"
)

// prober times TCP connections to the plan's targets, spread evenly over the
// interval so that each is tried once in it.
type prober struct {
	out chan liveproto.ProbeResult

	mu     sync.Mutex
	cancel context.CancelFunc
	dns    map[string]resolved
}

// Which addresses may be probed and how a name is looked up. Tests, which
// have only this machine to probe, replace them.
var (
	public = liveproto.Public
	lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
		return (&net.Resolver{PreferGo: true}).LookupNetIP(ctx, "ip", host)
	}
)

type resolved struct {
	ip    netip.Addr
	until time.Time
}

func newProber() *prober {
	return &prober{out: make(chan liveproto.ProbeResult, 2*liveproto.MaxTargets), dns: map[string]resolved{}}
}

// set replaces what is probed; nil stops probing.
func (p *prober) set(ctx context.Context, plan *liveproto.Plan) {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	if plan == nil || len(plan.Targets) == 0 {
		p.mu.Unlock()
		return
	}
	ctx, p.cancel = context.WithCancel(ctx)
	p.mu.Unlock()
	go p.run(ctx, *plan)
}

func (p *prober) run(ctx context.Context, plan liveproto.Plan) {
	step := time.Duration(plan.IntervalSec) * time.Second / time.Duration(len(plan.Targets))
	t := time.NewTicker(step)
	defer t.Stop()
	for i := 0; ; i = (i + 1) % len(plan.Targets) {
		go p.probe(ctx, plan.Targets[i])
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// address is where a target is now. A name is looked up every ten minutes; an
// answer that stops coming keeps the last address rather than reading as loss.
func (p *prober) address(ctx context.Context, host string) (netip.Addr, bool) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip, public(ip)
	}
	p.mu.Lock()
	known, ok := p.dns[host]
	p.mu.Unlock()
	if ok && time.Now().Before(known.until) {
		return known.ip, known.ip.IsValid()
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	ips, err := lookup(lookupCtx, host)
	cancel()
	next := resolved{ip: known.ip, until: time.Now().Add(30 * time.Second)}
	if err == nil {
		var pick netip.Addr
		for _, ip := range ips {
			ip = ip.Unmap()
			if public(ip) && (!pick.IsValid() || ip.Is4() && !pick.Is4()) {
				pick = ip
			}
		}
		if pick.IsValid() {
			next = resolved{ip: pick, until: time.Now().Add(10 * time.Minute)}
		}
	}
	p.mu.Lock()
	p.dns[host] = next
	p.mu.Unlock()
	return next.ip, next.ip.IsValid()
}

func (p *prober) probe(ctx context.Context, t liveproto.Target) {
	ip, ok := p.address(ctx, t.Host)
	if !ok {
		return
	}
	d := net.Dialer{Timeout: liveproto.ProbeTimeoutMs * time.Millisecond}
	began := time.Now()
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(t.Port)))
	r := liveproto.ProbeResult{Target: t.ID, RTT: liveproto.Lost}
	if err == nil {
		r.RTT = max(1, time.Since(began).Microseconds())
		conn.Close()
	} else if ctx.Err() != nil {
		return
	}
	select {
	case p.out <- r:
	default:
	}
}
