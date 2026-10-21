package batch

import "time"

// Clock is the time source of a batcher.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// NewTimer returns a timer that fires once after d.
	NewTimer(d time.Duration) Timer
}

// Timer fires once on its channel.
type Timer interface {
	// C returns the channel the fire time is sent on.
	C() <-chan time.Time
	// Stop prevents the timer from firing. It reports whether the timer was still pending.
	Stop() bool
}

// SystemClock returns the wall clock.
func SystemClock() Clock {
	return systemClock{}
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

func (systemClock) NewTimer(d time.Duration) Timer {
	return systemTimer{t: time.NewTimer(d)}
}

type systemTimer struct {
	t *time.Timer
}

func (s systemTimer) C() <-chan time.Time {
	return s.t.C
}

func (s systemTimer) Stop() bool {
	return s.t.Stop()
}
