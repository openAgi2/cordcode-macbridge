//go:build !darwin && !linux

package gobridge

func processCPUSeconds() (userSeconds float64, systemSeconds float64, ok bool) {
	return 0, 0, false
}
