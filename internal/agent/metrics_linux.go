//go:build linux

package agent

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"ctlvps/internal/agentproto"
	"ctlvps/internal/hostmetrics"
)

type cpuSample struct {
	idle, total uint64
	at          time.Time
}

// MetricsCollector reads /proc on Linux.
type MetricsCollector struct {
	prevCPU cpuSample
	prevNet struct {
		rx, tx int64
		at     time.Time
	}
	iface string
}

// NewMetricsCollector builds a collector.
func NewMetricsCollector() *MetricsCollector { return &MetricsCollector{} }

// Collect returns a metrics snapshot.
func (m *MetricsCollector) Collect() agentproto.Metrics {
	now := time.Now()
	var out agentproto.Metrics
	idle, total := hostmetrics.CPUTimes()
	if m.prevCPU.total > 0 && total > m.prevCPU.total {
		dt := float64(total - m.prevCPU.total)
		di := float64(idle - m.prevCPU.idle)
		out.CPUPercent = (1 - di/dt) * 100
	}
	m.prevCPU = cpuSample{idle: idle, total: total, at: now}

	out.Load1, out.Load5, _ = hostmetrics.Load()
	mem := hostmetrics.ReadMemory()
	out.MemTotal, out.MemUsed, out.SwapTotal, out.SwapUsed = mem.Total, mem.Used, mem.SwapTotal, mem.SwapUsed
	out.DiskTotal, out.DiskUsed = hostmetrics.Disk()
	out.UptimeSec = hostmetrics.Uptime()
	if m.iface == "" {
		m.iface = hostmetrics.DefaultInterface()
	}
	out.Interface = m.iface
	out.NetRx, out.NetTx = hostmetrics.NetCounters(m.iface)
	if !m.prevNet.at.IsZero() {
		dt := now.Sub(m.prevNet.at).Seconds()
		if dt > 0 && out.NetRx >= m.prevNet.rx && out.NetTx >= m.prevNet.tx {
			out.NetRxRate = int64(float64(out.NetRx-m.prevNet.rx) / dt)
			out.NetTxRate = int64(float64(out.NetTx-m.prevNet.tx) / dt)
		}
	}
	m.prevNet.rx, m.prevNet.tx, m.prevNet.at = out.NetRx, out.NetTx, now
	out.TCPConns, out.UDPConns = hostmetrics.Sockets()
	if entries, err := os.ReadDir("/proc"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				if _, err := strconv.Atoi(e.Name()); err == nil {
					out.Processes++
				}
			}
		}
	}
	out.Hostname, _ = os.Hostname()
	out.Kernel = hostmetrics.Kernel()
	out.Arch = runtime.GOARCH
	return out
}

// BootID identifies the current boot (counters reset on reboot).
func BootID() string {
	b, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(b))
}
