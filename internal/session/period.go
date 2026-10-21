package session

import "time"

const (
	// PollPeriod is how often the process compares model file identity.
	// The comparison is inode, size, and modification time. It does not hash file bytes.
	PollPeriod = 30 * time.Second

	// QuietPeriod is how long the process waits after the last change for one model before loading it.
	QuietPeriod = 100 * time.Millisecond
)
