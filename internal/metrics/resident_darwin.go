//go:build darwin

package metrics

import "golang.org/x/sys/unix"

// residentBytes is the peak resident size of this process, in bytes.
// The process collector omits resident memory on this system unless cgo is enabled.
func residentBytes() (float64, bool) {
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil || usage.Maxrss < 0 {
		return 0, false
	}
	return float64(usage.Maxrss), true
}
