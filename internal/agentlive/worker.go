// Package agentlive is the agent's live worker: a long-running unprivileged
// process that keeps one WebSocket open to the controller, sends a reading of
// the host every second or so, and times TCP connections to the probe targets
// the controller names. It reads only what any local user can read and has no
// way back into the root agent, so a controller that sends it nonsense can at
// worst stop the readings.
package agentlive

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net"
	"net/url"
	"time"

	"ctlvps/internal/liveproto"

	"golang.org/x/net/websocket"
)

// Config is what the worker is started with.
type Config struct {
	URL     string // the controller, https://host[:port]
	Token   string // the agent's bearer token
	Version string

	// TLS replaces the default client configuration; tests set it.
	TLS *tls.Config `json:"-"`
}

const (
	writeTimeout = 10 * time.Second
	// The controller writes at least every 25 seconds.
	readTimeout = 75 * time.Second
	maxBackoff  = time.Minute
)

// Run streams until ctx ends, reconnecting whenever the connection is lost.
func Run(ctx context.Context, cfg Config) error {
	if _, err := endpoint(cfg.URL); err != nil {
		return err
	}
	s := newSampler()
	backoff := time.Second
	for ctx.Err() == nil {
		began := time.Now()
		_ = session(ctx, cfg, s)
		if time.Since(began) > time.Minute {
			backoff = time.Second
		}
		wait := backoff + time.Duration(rand.Int64N(int64(backoff/2)+1))
		backoff = min(backoff*2, maxBackoff)
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
	return nil
}

func endpoint(base string) (*url.URL, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, errors.New("live channel requires an HTTPS controller URL")
	}
	return u, nil
}

func dial(ctx context.Context, cfg Config) (*websocket.Conn, error) {
	u, err := endpoint(cfg.URL)
	if err != nil {
		return nil, err
	}
	wc, err := websocket.NewConfig("wss://"+u.Host+liveproto.Path, "https://"+u.Host)
	if err != nil {
		return nil, err
	}
	wc.Header.Set("Authorization", "Bearer "+cfg.Token)
	wc.Header.Set("User-Agent", "ctlvps-agent/"+cfg.Version)
	tc := &tls.Config{}
	if cfg.TLS != nil {
		tc = cfg.TLS.Clone()
	}
	tc.NextProtos = []string{"http/1.1"}
	addr := u.Host
	if u.Port() == "" {
		addr = net.JoinHostPort(u.Hostname(), "443")
	}
	d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}, Config: tc}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	ws, err := websocket.NewClient(wc, conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	ws.MaxPayloadBytes = liveproto.MaxDownBytes
	return ws, nil
}

func send(ws *websocket.Conn, up liveproto.Up) error {
	b, err := json.Marshal(up)
	if err != nil {
		return err
	}
	_ = ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	return websocket.Message.Send(ws, string(b))
}

// session is one connection, from the handshake until it breaks.
func session(ctx context.Context, cfg Config, s *sampler) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ws, err := dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer ws.Close()
	host := s.host()
	if err := send(ws, liveproto.Up{Host: &host}); err != nil {
		return err
	}

	downs := make(chan liveproto.Down, 4)
	broken := make(chan error, 1)
	go func() {
		for {
			_ = ws.SetReadDeadline(time.Now().Add(readTimeout))
			var raw string
			if err := websocket.Message.Receive(ws, &raw); err != nil {
				broken <- err
				return
			}
			var d liveproto.Down
			if json.Unmarshal([]byte(raw), &d) != nil {
				broken <- errors.New("unreadable message from the controller")
				return
			}
			select {
			case downs <- d:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Probing belongs to the connection: with nobody to tell, nothing is timed.
	p := newProber()
	defer p.set(ctx, nil)

	interval := liveproto.IdleIntervalMs * time.Millisecond
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-broken:
			return err
		case d := <-downs:
			if d.IntervalMs > 0 {
				if next := time.Duration(liveproto.ClampInterval(d.IntervalMs)) * time.Millisecond; next != interval {
					interval = next
					tick.Reset(interval)
				}
			}
			if d.Plan != nil && d.Plan.Validate() == nil {
				p.set(ctx, d.Plan)
			}
		case <-tick.C:
			sample := s.read()
			if err := send(ws, liveproto.Up{Sample: &sample}); err != nil {
				return err
			}
		case r := <-p.out:
			results := []liveproto.ProbeResult{r}
			for more := true; more && len(results) < liveproto.MaxTargets; {
				select {
				case r := <-p.out:
					results = append(results, r)
				default:
					more = false
				}
			}
			if err := send(ws, liveproto.Up{Probes: results}); err != nil {
				return err
			}
		}
	}
}
