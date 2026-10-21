package session

import (
	"testing"
	"time"
)

func TestReloadPeriods(t *testing.T) {
	if PollPeriod != 30*time.Second {
		t.Fatalf("poll %s", PollPeriod)
	}
	if QuietPeriod != 100*time.Millisecond {
		t.Fatalf("quiet %s", QuietPeriod)
	}
}
