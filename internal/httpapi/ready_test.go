package httpapi

import "testing"

func TestAnyReady(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		states []State
		want   bool
	}{
		{name: "empty", want: false},
		{name: "nil", states: nil, want: false},
		{name: "loading", states: []State{StateLoading}, want: false},
		{name: "unavailable", states: []State{StateUnavailable}, want: false},
		{name: "loading and unavailable", states: []State{StateLoading, StateUnavailable}, want: false},
		{name: "ready and unavailable", states: []State{StateReady, StateUnavailable}, want: true},
		{name: "single ready", states: []State{StateReady}, want: true},
		{name: "ready and loading", states: []State{StateLoading, StateReady}, want: true},
		{name: "ready loading and unavailable", states: []State{StateLoading, StateUnavailable, StateReady}, want: true},
		{name: "unknown", states: []State{State("UNLOADING")}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := AnyReady(tc.states...); got != tc.want {
				t.Fatalf("AnyReady = %v, want %v", got, tc.want)
			}
		})
	}
}
