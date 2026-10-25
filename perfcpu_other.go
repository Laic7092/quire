//go:build !unix && !windows

package main

// perfCPU is the CPU time the process has used on all its threads, in
// nanoseconds. This platform has no reading for it, so the log leaves the
// frame's CPU cost out rather than guessing.
func perfCPU() int64 { return 0 }
