//go:build unix

package main

import "syscall"

// cpuTime is the process's CPU time over all its threads, in nanoseconds.
type cpuTime = int64

// readCPU returns the CPU time the process has used so far. It is the frame
// cost that a refresh rate's budget is spent from, unlike wall time, which a
// display's refresh rate caps.
func readCPU() cpuTime {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return cpuTime(ru.Utime.Nano() + ru.Stime.Nano())
}
