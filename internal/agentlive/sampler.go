package agentlive

import (
	"runtime"
	"time"

	"ctlvps/internal/hostmetrics"
	"ctlvps/internal/liveproto"
)

// sampler turns the host's counters into readings: CPU use and network rates
// are what changed since the reading before.
type sampler struct {
	iface    string
	cpuIdle  uint64
	cpuTotal uint64
	rx, tx   int64
	at       time.Time

	// The root filesystem does not fill up within seconds.
	diskAt              time.Time
	diskTotal, diskUsed int64
}

// newSampler takes a first reading, so that the first one sent has rates.
func newSampler() *sampler {
	s := &sampler{}
	s.read()
	return s
}

func (s *sampler) host() liveproto.Host {
	model, cores := hostmetrics.CPU()
	return liveproto.Host{OS: hostmetrics.OS(), Kernel: hostmetrics.Kernel(), Arch: runtime.GOARCH, Virt: hostmetrics.Virt(), CPUModel: model, Cores: cores}
}

func (s *sampler) read() liveproto.Sample {
	now := time.Now()
	out := liveproto.Sample{Uptime: hostmetrics.Uptime()}

	idle, total := hostmetrics.CPUTimes()
	if s.cpuTotal > 0 && total > s.cpuTotal && idle >= s.cpuIdle {
		out.CPU = (1 - float64(idle-s.cpuIdle)/float64(total-s.cpuTotal)) * 100
	}
	s.cpuIdle, s.cpuTotal = idle, total

	mem := hostmetrics.ReadMemory()
	out.MemUsed, out.MemTotal, out.SwapUsed, out.SwapTotal = mem.Used, mem.Total, mem.SwapUsed, mem.SwapTotal
	out.Load1, out.Load5, out.Load15 = hostmetrics.Load()
	out.TCP, out.UDP = hostmetrics.Sockets()

	if now.Sub(s.diskAt) >= 10*time.Second {
		s.diskTotal, s.diskUsed = hostmetrics.Disk()
		s.diskAt = now
	}
	out.DiskTotal, out.DiskUsed = s.diskTotal, s.diskUsed

	if s.iface == "" {
		s.iface = hostmetrics.DefaultInterface()
	}
	rx, tx := hostmetrics.NetCounters(s.iface)
	if dt := now.Sub(s.at).Seconds(); !s.at.IsZero() && dt > 0 && rx >= s.rx && tx >= s.tx {
		out.RxRate = int64(float64(rx-s.rx) / dt)
		out.TxRate = int64(float64(tx-s.tx) / dt)
	}
	s.rx, s.tx, s.at = rx, tx, now
	return out
}
