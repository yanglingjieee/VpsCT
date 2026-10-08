//go:build linux

package hostmetrics

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// The live worker reads the host as an unprivileged process that has given
// up being dumpable, which changes who owns its own /proc entries. Everything
// it reports must still be readable then.
func TestReadableByAHardenedProcess(t *testing.T) {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	idle, total := CPUTimes()
	mem := ReadMemory()
	l1, l5, l15 := Load()
	diskTotal, diskUsed := Disk()
	iface := DefaultInterface()
	rx, tx := NetCounters(iface)
	tcp, udp := Sockets()
	model, cores := CPU()
	t.Logf("uid=%d cpu idle=%d total=%d mem=%+v load=%.2f %.2f %.2f disk=%d/%d uptime=%d iface=%q rx=%d tx=%d tcp=%d udp=%d", os.Geteuid(), idle, total, mem, l1, l5, l15, diskUsed, diskTotal, Uptime(), iface, rx, tx, tcp, udp)
	t.Logf("os=%q kernel=%q virt=%q cpu=%q cores=%d", OS(), Kernel(), Virt(), model, cores)
	if total == 0 || idle > total {
		t.Error("CPU times unreadable")
	}
	if mem.Total <= 0 || mem.Used <= 0 || mem.Used > mem.Total {
		t.Errorf("memory unreadable: %+v", mem)
	}
	if diskTotal <= 0 || diskUsed <= 0 || diskUsed > diskTotal {
		t.Errorf("disk unreadable: %d/%d", diskUsed, diskTotal)
	}
	if Uptime() <= 0 || Kernel() == "" || OS() == "" || cores < 1 {
		t.Error("host description unreadable")
	}
	// A machine without a network (a build container) has no default route
	// and nothing to count; one with a route must show traffic on it.
	if iface != "" && rx+tx == 0 {
		t.Errorf("no traffic counted on %s", iface)
	}
	if _, err := os.Stat("/proc/net/sockstat"); err == nil && tcp+udp == 0 && iface != "" {
		t.Error("socket counts unreadable")
	}
}
