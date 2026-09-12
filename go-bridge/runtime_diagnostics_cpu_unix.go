//go:build darwin || linux

package gobridge

import "syscall"

func processCPUSeconds() (userSeconds float64, systemSeconds float64, ok bool) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, 0, false
	}
	timevalSeconds := func(v syscall.Timeval) float64 {
		return float64(v.Sec) + float64(v.Usec)/1_000_000
	}
	return timevalSeconds(usage.Utime), timevalSeconds(usage.Stime), true
}
