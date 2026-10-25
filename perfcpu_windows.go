//go:build windows

package main

import "syscall"

// perfCPU is the CPU time the process has used on all its threads, in
// nanoseconds. It is what a frame really cost the machine: wall time is capped
// by the display's refresh, CPU time is not, so this is the number to compare
// between two machines.
func perfCPU() int64 {
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0
	}
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return 0
	}
	// A Filetime counts 100-nanosecond intervals.
	return kernel.Nanoseconds() + user.Nanoseconds()
}
