package batch

import "fmt"

// InvalidError is a request the client must change before it can run.
// Its text names tensors and parameters. It never contains tensor values.
type InvalidError struct {
	// Reason explains what is wrong with the request.
	Reason string
}

// Error returns the reason.
func (e *InvalidError) Error() string {
	if e == nil {
		return ""
	}
	return e.Reason
}

func invalidf(format string, args ...any) error {
	return &InvalidError{Reason: fmt.Sprintf(format, args...)}
}
