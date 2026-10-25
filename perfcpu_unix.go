//go:build unix

package main

import "syscall"

// perfCPU is the CPU time the process has used on all its threads, in
// nanoseconds. It is what a frame really cost the machine: wall time is capped
// by the display's refresh, CPU time is not, so this is the number to compare
// between two machines.
func perfCPU() int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return ru.Utime.Nano() + ru.Stime.Nano()
}
