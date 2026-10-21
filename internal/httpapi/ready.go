package httpapi

// State is the serving condition of one model.
type State string

const (
	// StateReady means the model can serve.
	StateReady State = "READY"
	// StateLoading means the model is still loading.
	StateLoading State = "LOADING"
	// StateUnavailable means the model cannot serve.
	StateUnavailable State = "UNAVAILABLE"
)

// AnyReady reports whether at least one model can serve.
// Loading and unavailable do not count. An empty set is not ready.
func AnyReady(states ...State) bool {
	for _, state := range states {
		if state == StateReady {
			return true
		}
	}
	return false
}
