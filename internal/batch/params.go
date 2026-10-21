package batch

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
)

// ParamKind is the type of one parameter value.
type ParamKind int

const (
	// ParamOther is a value that is not a bool, number, or string.
	ParamOther ParamKind = iota
	// ParamBool is a bool.
	ParamBool
	// ParamInt is a signed integer.
	ParamInt
	// ParamUint is an unsigned integer above the signed range.
	ParamUint
	// ParamFloat is a number with a fraction or an exponent.
	ParamFloat
	// ParamString is a string.
	ParamString
)

// Param is one parameter value from a request or a tensor.
type Param struct {
	kind ParamKind
	b    bool
	i    int64
	u    uint64
	f    float64
	s    string
}

// BoolParam returns a bool parameter.
func BoolParam(v bool) Param { return Param{kind: ParamBool, b: v} }

// IntParam returns a signed integer parameter.
func IntParam(v int64) Param { return Param{kind: ParamInt, i: v} }

// UintParam returns an unsigned integer parameter.
func UintParam(v uint64) Param { return Param{kind: ParamUint, u: v} }

// FloatParam returns a floating-point parameter.
func FloatParam(v float64) Param { return Param{kind: ParamFloat, f: v} }

// StringParam returns a string parameter.
func StringParam(v string) Param { return Param{kind: ParamString, s: v} }

// Kind returns the value type.
func (p Param) Kind() ParamKind { return p.kind }

func (p Param) int64Value() (int64, bool) {
	switch p.kind {
	case ParamInt:
		return p.i, true
	case ParamUint:
		if p.u <= math.MaxInt64 {
			return int64(p.u), true
		}
	default:
	}
	return 0, false
}

func (p Param) boolValue() (bool, bool) {
	return p.b, p.kind == ParamBool
}

// sequenceKeys are rejected because there is no sequence batcher.
// Running them as stateless requests would give wrong results.
var sequenceKeys = []string{"sequence_id", "sequence_start", "sequence_end"}

func rejectSequence(where string, p map[string]Param) error {
	for _, k := range sequenceKeys {
		if _, ok := p[k]; ok {
			return invalidf("%s parameter %q is not supported", where, k)
		}
	}
	return nil
}

// RequestParameters are the honored request parameters. Every other key is dropped.
type RequestParameters struct {
	// Timeout is the longest wait in the queue, in microseconds. Zero means the queue does not expire the request.
	Timeout int64
	// Priority is accepted and does not reorder the queue.
	Priority int64
	// BinaryOutput is binary_data_output.
	BinaryOutput bool
	// BinaryOutputSet reports whether the request set binary_data_output.
	BinaryOutputSet bool
}

// ParseRequestParameters classifies the parameters of an infer request.
func ParseRequestParameters(p map[string]Param) (RequestParameters, error) {
	var out RequestParameters
	if err := rejectSequence("request", p); err != nil {
		return out, err
	}
	if v, ok := p["timeout"]; ok {
		n, ok := v.int64Value()
		if !ok {
			return out, invalidf("request parameter %q must be an integer", "timeout")
		}
		if n < 0 {
			return out, invalidf("request parameter %q must not be negative", "timeout")
		}
		out.Timeout = n
	}
	if v, ok := p["priority"]; ok {
		n, ok := v.int64Value()
		if !ok {
			return out, invalidf("request parameter %q must be an integer", "priority")
		}
		out.Priority = n
	}
	if v, ok := p["binary_data_output"]; ok {
		b, ok := v.boolValue()
		if !ok {
			return out, invalidf("request parameter %q must be a bool", "binary_data_output")
		}
		out.BinaryOutput, out.BinaryOutputSet = b, true
	}
	return out, nil
}

// InputParameters are the honored parameters of one request input.
type InputParameters struct {
	// BinaryDataSize is the byte size of the input in the binary suffix.
	BinaryDataSize int64
	// Binary reports whether the input set binary_data_size.
	Binary bool
}

// ParseInputParameters classifies the parameters of the input called name.
func ParseInputParameters(name string, p map[string]Param) (InputParameters, error) {
	var out InputParameters
	if err := rejectSequence("input "+strconv.Quote(name), p); err != nil {
		return out, err
	}
	if v, ok := p["binary_data_size"]; ok {
		n, ok := v.int64Value()
		if !ok || n < 0 {
			return out, invalidf("input %q parameter %q must be a non-negative integer", name, "binary_data_size")
		}
		out.BinaryDataSize, out.Binary = n, true
	}
	return out, nil
}

// OutputParameters are the honored parameters of one requested output.
type OutputParameters struct {
	// Binary is binary_data.
	Binary bool
	// BinarySet reports whether the output set binary_data.
	BinarySet bool
}

// ParseOutputParameters classifies the parameters of the requested output called name.
func ParseOutputParameters(name string, p map[string]Param) (OutputParameters, error) {
	var out OutputParameters
	if err := rejectSequence("output "+strconv.Quote(name), p); err != nil {
		return out, err
	}
	if v, ok := p["binary_data"]; ok {
		b, ok := v.boolValue()
		if !ok {
			return out, invalidf("output %q parameter %q must be a bool", name, "binary_data")
		}
		out.Binary, out.BinarySet = b, true
	}
	return out, nil
}

// jsonParams reads a JSON parameters object. An absent or null object is empty.
func jsonParams(where string, raw json.RawMessage) (map[string]Param, error) {
	if absent(raw) {
		return nil, nil
	}
	raw = bytes.TrimSpace(raw)
	var m map[string]json.RawMessage
	if raw[0] != '{' || json.Unmarshal(raw, &m) != nil {
		return nil, invalidf("%s parameters must be a JSON object", where)
	}
	out := make(map[string]Param, len(m))
	for k, v := range m {
		out[k] = jsonParam(v)
	}
	return out, nil
}

func jsonParam(raw json.RawMessage) Param {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return Param{}
	}
	switch c := raw[0]; {
	case c == 't' || c == 'f':
		return BoolParam(c == 't')
	case c == '"':
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return StringParam(s)
		}
	case c == '-' || (c >= '0' && c <= '9'):
		lit := string(raw)
		if n, err := strconv.ParseInt(lit, 10, 64); err == nil {
			return IntParam(n)
		}
		if n, err := strconv.ParseUint(lit, 10, 64); err == nil {
			return UintParam(n)
		}
		f, _ := strconv.ParseFloat(lit, 64)
		return FloatParam(f)
	default:
	}
	return Param{}
}

// absent reports whether a JSON field is missing or null.
func absent(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || string(raw) == "null"
}
