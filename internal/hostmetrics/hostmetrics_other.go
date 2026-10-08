//go:build !linux

package hostmetrics

import "runtime"

// Only Linux hosts run the agent; elsewhere, for development, the readings
// are empty.

func CPUTimes() (idle, total uint64)          { return 0, 0 }
func ReadMemory() Memory                      { return Memory{} }
func Load() (l1, l5, l15 float64)             { return 0, 0, 0 }
func Disk() (total, used int64)               { return 0, 0 }
func Uptime() int64                           { return 0 }
func DefaultInterface() string                { return "" }
func NetCounters(iface string) (rx, tx int64) { return 0, 0 }
func Sockets() (tcp, udp int)                 { return 0, 0 }
func Kernel() string                          { return runtime.GOOS }
func OS() string                              { return runtime.GOOS }
func CPU() (model string, cores int)          { return "", runtime.NumCPU() }
func Virt() string                            { return "" }
