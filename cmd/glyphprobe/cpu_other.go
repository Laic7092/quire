//go:build !unix

package main

// cpuTime is the process's CPU time, in nanoseconds, which this platform does
// not report here: the wall time a frame still measures the glide there.
type cpuTime = int64

func readCPU() cpuTime { return 0 }
