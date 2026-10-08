//go:build linux

package hostmetrics

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func read(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// CPUTimes is the idle (with iowait) and total jiffies of all cores since boot.
func CPUTimes() (idle, total uint64) {
	for _, line := range strings.Split(read("/proc/stat"), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		f := strings.Fields(line)
		for i := 1; i < len(f); i++ {
			v, _ := strconv.ParseUint(f[i], 10, 64)
			total += v
			if i == 4 || i == 5 {
				idle += v
			}
		}
		return idle, total
	}
	return 0, 0
}

// ReadMemory reads the host's memory and swap.
func ReadMemory() Memory {
	mi := map[string]int64{}
	for _, line := range strings.Split(read("/proc/meminfo"), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, _ := strconv.ParseInt(f[0], 10, 64)
		mi[k] = n * 1024
	}
	m := Memory{Total: mi["MemTotal"], SwapTotal: mi["SwapTotal"], SwapUsed: mi["SwapTotal"] - mi["SwapFree"]}
	if avail, ok := mi["MemAvailable"]; ok {
		m.Used = m.Total - avail
	} else {
		m.Used = m.Total - mi["MemFree"] - mi["Buffers"] - mi["Cached"]
	}
	return m
}

// Load is the 1, 5 and 15 minute load average.
func Load() (l1, l5, l15 float64) {
	f := strings.Fields(read("/proc/loadavg"))
	if len(f) >= 3 {
		l1, _ = strconv.ParseFloat(f[0], 64)
		l5, _ = strconv.ParseFloat(f[1], 64)
		l15, _ = strconv.ParseFloat(f[2], 64)
	}
	return
}

// Disk is the size and use of the root filesystem in bytes.
func Disk() (total, used int64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err != nil {
		return 0, 0
	}
	total = int64(st.Blocks) * int64(st.Bsize)
	return total, total - int64(st.Bavail)*int64(st.Bsize)
}

// Uptime is seconds since boot.
func Uptime() int64 {
	if f := strings.Fields(read("/proc/uptime")); len(f) >= 1 {
		up, _ := strconv.ParseFloat(f[0], 64)
		return int64(up)
	}
	return 0
}

// DefaultInterface is the interface of the default IPv4 route.
func DefaultInterface() string {
	for i, line := range strings.Split(read("/proc/net/route"), "\n") {
		if i == 0 {
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == "00000000" {
			return f[0]
		}
	}
	return ""
}

// NetCounters is the bytes received and sent since boot on the counted
// interfaces.
func NetCounters(iface string) (rx, tx int64) {
	for i, line := range strings.Split(read("/proc/net/dev"), "\n") {
		if i < 2 {
			continue
		}
		name, rest, ok := strings.Cut(line, ":")
		if !ok || !Counted(strings.TrimSpace(name), iface) {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseInt(f[0], 10, 64)
		t, _ := strconv.ParseInt(f[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx
}

// Sockets is the TCP and UDP sockets in use, IPv4 and IPv6 together. The
// kernel keeps these counts, so reading them costs the same however many
// connections a proxy holds.
func Sockets() (tcp, udp int) {
	for _, p := range []string{"/proc/net/sockstat", "/proc/net/sockstat6"} {
		for _, line := range strings.Split(read(p), "\n") {
			f := strings.Fields(line)
			if len(f) < 3 || f[1] != "inuse" {
				continue
			}
			n, _ := strconv.Atoi(f[2])
			switch f[0] {
			case "TCP:", "TCP6:":
				tcp += n
			case "UDP:", "UDP6:":
				udp += n
			}
		}
	}
	return tcp, udp
}

// Kernel is the running kernel's release.
func Kernel() string { return strings.TrimSpace(read("/proc/sys/kernel/osrelease")) }

// OS is the distribution's name as it calls itself.
func OS() string {
	for _, line := range strings.Split(read("/etc/os-release"), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return "Linux"
}

// ARM cores name themselves by part number only.
var armParts = map[string]string{"0xd0c": "Neoverse-N1", "0xd40": "Neoverse-V1", "0xd49": "Neoverse-N2", "0xd4f": "Neoverse-V2", "0xd03": "Cortex-A53", "0xd08": "Cortex-A72", "0xd0b": "Cortex-A76"}

// CPU is the processor model and the number of cores the host may use. A
// container is shown the cores it was given.
func CPU() (model string, cores int) {
	part := ""
	for _, line := range strings.Split(read("/proc/cpuinfo"), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			cores++
		case "model name":
			if model == "" {
				model = strings.Join(strings.Fields(v), " ")
			}
		case "CPU part":
			part = v
		}
	}
	if model == "" {
		model = armParts[part]
	}
	if cores == 0 {
		cores = runtime.NumCPU()
	}
	return model, cores
}

// Virt names what the host runs in: a container manager or a hypervisor.
// Empty when nothing says so, which is what bare metal looks like.
func Virt() string {
	if v := strings.TrimSpace(read("/run/systemd/container")); v != "" {
		return v
	}
	if _, err := os.Stat("/proc/user_beancounters"); err == nil {
		return "openvz"
	}
	dmi := strings.ToLower(read("/sys/class/dmi/id/sys_vendor") + " " + read("/sys/class/dmi/id/product_name"))
	for _, known := range [][2]string{{"kvm", "kvm"}, {"qemu", "kvm"}, {"alibaba", "kvm"}, {"openstack", "kvm"}, {"google", "kvm"}, {"digitalocean", "kvm"}, {"vultr", "kvm"}, {"amazon", "kvm"}, {"vmware", "vmware"}, {"microsoft", "hyper-v"}, {"xen", "xen"}, {"virtualbox", "virtualbox"}, {"parallels", "parallels"}} {
		if strings.Contains(dmi, known[0]) {
			return known[1]
		}
	}
	for _, line := range strings.Split(read("/proc/cpuinfo"), "\n") {
		if strings.HasPrefix(line, "flags") && strings.Contains(" "+line+" ", " hypervisor ") {
			return "vm"
		}
	}
	return ""
}
