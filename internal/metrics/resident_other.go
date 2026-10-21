//go:build !darwin

package metrics

// residentBytes reports nothing. The process collector provides resident memory on this system.
func residentBytes() (float64, bool) {
	return 0, false
}
