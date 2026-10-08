package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"ctlvps/internal/domain"
	"ctlvps/internal/liveproto"
)

// Live measurements are kept at two resolutions, in seconds per row.
const (
	LiveMinute = 60
	LiveHour   = 3600
)

// MetricPoint is what a server's live readings came to over one bucket of
// time: averages, and the peaks of what is read for its peaks.
type MetricPoint struct {
	ServerID  int64   `json:"-"`
	TS        int64   `json:"ts"` // unix seconds, start of the bucket
	N         int     `json:"-"`  // readings in it
	CPU       float64 `json:"cpu"`
	CPUMax    float64 `json:"cpu_max"`
	MemUsed   int64   `json:"mem_used"`
	MemTotal  int64   `json:"mem_total"`
	SwapUsed  int64   `json:"swap_used"`
	DiskUsed  int64   `json:"disk_used"`
	DiskTotal int64   `json:"disk_total"`
	Load1     float64 `json:"load1"`
	RxRate    int64   `json:"rx_rate"`
	TxRate    int64   `json:"tx_rate"`
	RxMax     int64   `json:"rx_max"`
	TxMax     int64   `json:"tx_max"`
	TCP       int     `json:"tcp"`
	UDP       int     `json:"udp"`
}

// ProbePoint is what a server's probes of one target came to over one bucket
// of time. The round trip times, in microseconds, are of the probes that were
// answered: Sent less Lost of them.
type ProbePoint struct {
	ServerID int64 `json:"-"`
	TargetID int64 `json:"-"`
	TS       int64 `json:"ts"`
	Sent     int   `json:"sent"`
	Lost     int   `json:"lost"`
	RTTSum   int64 `json:"rtt_sum"`
	RTTMin   int64 `json:"rtt_min"`
	RTTMax   int64 `json:"rtt_max"`
}

// Avg is the mean round trip time in microseconds, 0 when nothing answered.
func (p ProbePoint) Avg() int64 {
	if n := p.Sent - p.Lost; n > 0 {
		return p.RTTSum / int64(n)
	}
	return 0
}

// Merge adds what another bucket saw.
func (p *ProbePoint) Merge(o ProbePoint) {
	if o.Sent > o.Lost && (p.Sent == p.Lost || o.RTTMin < p.RTTMin) {
		p.RTTMin = o.RTTMin
	}
	p.RTTMax = max(p.RTTMax, o.RTTMax)
	p.Sent, p.Lost, p.RTTSum = p.Sent+o.Sent, p.Lost+o.Lost, p.RTTSum+o.RTTSum
}

// ---- probe targets ----

// ListProbeTargets returns the targets in the order the operator put them.
func (s *Store) ListProbeTargets(ctx context.Context) ([]domain.ProbeTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,host,port,carrier,on_card,sort_order FROM probe_targets ORDER BY sort_order,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ProbeTarget{}
	for rows.Next() {
		var t domain.ProbeTarget
		var onCard int
		if err := rows.Scan(&t.ID, &t.Name, &t.Host, &t.Port, &t.Carrier, &onCard, &t.SortOrder); err != nil {
			return nil, err
		}
		t.OnCard = onCard == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

// ReplaceProbeTargets makes the list the set of targets, in its order. A
// target that stays at the same address keeps its identity and with it what
// was measured; one that is no longer listed goes with its measurements.
func (s *Store) ReplaceProbeTargets(ctx context.Context, list []domain.ProbeTarget) ([]domain.ProbeTarget, error) {
	if len(list) > liveproto.MaxTargets {
		return nil, errors.New("too many probe targets")
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		type addr struct {
			host string
			port int
		}
		have := map[addr]int64{}
		rows, err := tx.QueryContext(ctx, `SELECT id,host,port FROM probe_targets`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var a addr
			if err := rows.Scan(&id, &a.host, &a.port); err != nil {
				rows.Close()
				return err
			}
			have[a] = id
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for i, t := range list {
			a := addr{t.Host, t.Port}
			if id, ok := have[a]; ok {
				if _, err := tx.ExecContext(ctx, `UPDATE probe_targets SET name=?, carrier=?, on_card=?, sort_order=? WHERE id=?`, t.Name, t.Carrier, b2i(t.OnCard), i+1, id); err != nil {
					return err
				}
				delete(have, a)
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO probe_targets(name,host,port,carrier,on_card,sort_order,created_at) VALUES (?,?,?,?,?,?,?)`, t.Name, t.Host, t.Port, t.Carrier, b2i(t.OnCard), i+1, fmtTime(s.Now())); err != nil {
				return err
			}
		}
		for _, id := range have {
			if _, err := tx.ExecContext(ctx, `DELETE FROM probe_targets WHERE id=?`, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.ListProbeTargets(ctx)
}

// ---- what the host is ----

// SetAgentHost records what a server's live worker says the host is.
func (s *Store) SetAgentHost(ctx context.Context, serverID int64, h liveproto.Host) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET host=? WHERE server_id=?`, jsonStr(h), serverID)
	return err
}

// AgentHosts is what each server's live worker last said the host is.
func (s *Store) AgentHosts(ctx context.Context) (map[int64]liveproto.Host, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT server_id,host FROM agents WHERE host NOT IN ('','{}')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]liveproto.Host{}
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var h liveproto.Host
		if json.Unmarshal([]byte(raw), &h) == nil {
			out[id] = h
		}
	}
	return out, rows.Err()
}

// ---- measurements ----

// AddLiveMinutes stores finished minutes and brings the hours they fall in
// up to date. An hour is always derived from its minutes, so storing a minute
// twice counts it once.
func (s *Store) AddLiveMinutes(ctx context.Context, metrics []MetricPoint, probes []ProbePoint) error {
	if len(metrics) == 0 && len(probes) == 0 {
		return nil
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		type hourOf struct{ server, target, hour int64 }
		metricHours, probeHours := map[hourOf]bool{}, map[hourOf]bool{}
		for _, m := range metrics {
			// A server deleted since the reading was taken has nowhere to keep it.
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO server_metrics(server_id,res,ts,n,cpu,cpu_max,mem_used,mem_total,swap_used,disk_used,disk_total,load1,rx_rate,tx_rate,rx_max,tx_max,tcp,udp)
				SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM servers WHERE id=?)`,
				m.ServerID, LiveMinute, m.TS, m.N, m.CPU, m.CPUMax, m.MemUsed, m.MemTotal, m.SwapUsed, m.DiskUsed, m.DiskTotal, m.Load1, m.RxRate, m.TxRate, m.RxMax, m.TxMax, m.TCP, m.UDP, m.ServerID); err != nil {
				return err
			}
			metricHours[hourOf{m.ServerID, 0, m.TS - m.TS%LiveHour}] = true
		}
		for _, p := range probes {
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO probe_stats(server_id,target_id,res,ts,sent,lost,rtt_sum,rtt_min,rtt_max)
				SELECT ?,?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM servers WHERE id=?) AND EXISTS(SELECT 1 FROM probe_targets WHERE id=?)`,
				p.ServerID, p.TargetID, LiveMinute, p.TS, p.Sent, p.Lost, p.RTTSum, p.RTTMin, p.RTTMax, p.ServerID, p.TargetID); err != nil {
				return err
			}
			probeHours[hourOf{p.ServerID, p.TargetID, p.TS - p.TS%LiveHour}] = true
		}
		for h := range metricHours {
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO server_metrics(server_id,res,ts,n,cpu,cpu_max,mem_used,mem_total,swap_used,disk_used,disk_total,load1,rx_rate,tx_rate,rx_max,tx_max,tcp,udp)
				SELECT server_id,?,?,SUM(n),SUM(cpu*n)/SUM(n),MAX(cpu_max),SUM(mem_used*n)/SUM(n),MAX(mem_total),SUM(swap_used*n)/SUM(n),SUM(disk_used*n)/SUM(n),MAX(disk_total),SUM(load1*n)/SUM(n),SUM(rx_rate*n)/SUM(n),SUM(tx_rate*n)/SUM(n),MAX(rx_max),MAX(tx_max),SUM(tcp*n)/SUM(n),SUM(udp*n)/SUM(n)
				FROM server_metrics WHERE server_id=? AND res=? AND ts>=? AND ts<? GROUP BY server_id`,
				LiveHour, h.hour, h.server, LiveMinute, h.hour, h.hour+LiveHour); err != nil {
				return err
			}
		}
		for h := range probeHours {
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO probe_stats(server_id,target_id,res,ts,sent,lost,rtt_sum,rtt_min,rtt_max)
				SELECT server_id,target_id,?,?,SUM(sent),SUM(lost),SUM(rtt_sum),COALESCE(MIN(CASE WHEN sent>lost THEN rtt_min END),0),MAX(rtt_max)
				FROM probe_stats WHERE server_id=? AND target_id=? AND res=? AND ts>=? AND ts<? GROUP BY server_id,target_id`,
				LiveHour, h.hour, h.server, h.target, LiveMinute, h.hour, h.hour+LiveHour); err != nil {
				return err
			}
		}
		return nil
	})
}

// ServerMetrics returns a server's readings from one resolution, gathered
// into buckets of step seconds, oldest first.
func (s *Store) ServerMetrics(ctx context.Context, serverID int64, res int, from, to time.Time, step int64) ([]MetricPoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT (ts/?)*? AS b,SUM(n),SUM(cpu*n)/SUM(n),MAX(cpu_max),SUM(mem_used*n)/SUM(n),MAX(mem_total),SUM(swap_used*n)/SUM(n),SUM(disk_used*n)/SUM(n),MAX(disk_total),SUM(load1*n)/SUM(n),SUM(rx_rate*n)/SUM(n),SUM(tx_rate*n)/SUM(n),MAX(rx_max),MAX(tx_max),SUM(tcp*n)/SUM(n),SUM(udp*n)/SUM(n)
		FROM server_metrics WHERE server_id=? AND res=? AND ts>=? AND ts<? AND n>0 GROUP BY b ORDER BY b`, step, step, serverID, res, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MetricPoint{}
	for rows.Next() {
		m := MetricPoint{ServerID: serverID}
		if err := rows.Scan(&m.TS, &m.N, &m.CPU, &m.CPUMax, &m.MemUsed, &m.MemTotal, &m.SwapUsed, &m.DiskUsed, &m.DiskTotal, &m.Load1, &m.RxRate, &m.TxRate, &m.RxMax, &m.TxMax, &m.TCP, &m.UDP); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ProbeStats returns probe measurements from one resolution, gathered into
// buckets of step seconds, oldest first: of one server, or of every server
// when serverID is 0.
func (s *Store) ProbeStats(ctx context.Context, serverID int64, res int, from, to time.Time, step int64) ([]ProbePoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT server_id,target_id,(ts/?)*? AS b,SUM(sent),SUM(lost),SUM(rtt_sum),COALESCE(MIN(CASE WHEN sent>lost THEN rtt_min END),0),MAX(rtt_max)
		FROM probe_stats WHERE (?=0 OR server_id=?) AND res=? AND ts>=? AND ts<? GROUP BY server_id,target_id,b ORDER BY b`, step, step, serverID, serverID, res, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProbePoint{}
	for rows.Next() {
		var p ProbePoint
		if err := rows.Scan(&p.ServerID, &p.TargetID, &p.TS, &p.Sent, &p.Lost, &p.RTTSum, &p.RTTMin, &p.RTTMax); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProbeKey names one server's probes of one target.
type ProbeKey struct{ ServerID, TargetID int64 }

// ProbeBaselines is the usual round trip time of each server to each target,
// in microseconds: the median of its hourly means since from. Hours in which
// nothing answered say nothing about how long an answer takes; a pair with
// fewer than minHours of them has no baseline yet.
func (s *Store) ProbeBaselines(ctx context.Context, from time.Time, minHours int) (map[ProbeKey]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT server_id,target_id,rtt_sum/(sent-lost) FROM probe_stats WHERE res=? AND ts>=? AND sent>lost`, LiveHour, from.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hours := map[ProbeKey][]int64{}
	for rows.Next() {
		var k ProbeKey
		var avg int64
		if err := rows.Scan(&k.ServerID, &k.TargetID, &avg); err != nil {
			return nil, err
		}
		hours[k] = append(hours[k], avg)
	}
	out := map[ProbeKey]int64{}
	for k, v := range hours {
		if len(v) < minHours {
			continue
		}
		sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
		out[k] = v[len(v)/2]
	}
	return out, rows.Err()
}

// PruneLive drops measurements older than each resolution is kept for.
func (s *Store) PruneLive(ctx context.Context, minutes, hours time.Duration) error {
	now := s.Now()
	for _, table := range []string{"server_metrics", "probe_stats"} {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM `+table+` WHERE (res=? AND ts<?) OR (res=? AND ts<?)`, LiveMinute, now.Add(-minutes).Unix(), LiveHour, now.Add(-hours).Unix()); err != nil {
			return err
		}
	}
	return nil
}
