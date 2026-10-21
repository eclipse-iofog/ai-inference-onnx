package batch

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

// NoHeaderLength means the request had no Inference-Header-Content-Length header.
const NoHeaderLength int64 = -1

// maxExactJSONInteger is the largest integer magnitude a JSON number carries exactly.
const maxExactJSONInteger = 1 << 53

// Request is one decoded infer request.
type Request struct {
	// ID is echoed on the response. It is empty when the request omitted it.
	ID string
	// Parameters are the honored request parameters.
	Parameters RequestParameters
	// Inputs are in model order.
	Inputs []Tensor
	// Outputs are the outputs to return, in the order the response lists them.
	Outputs []RequestedOutput
}

// RequestedOutput is one output the response returns.
type RequestedOutput struct {
	// Name is the model output name.
	Name string
	// Datatype is the model output datatype.
	Datatype Datatype
	// Binary reports whether the output is returned after the JSON instead of as JSON values.
	Binary bool
}

// DecodeHTTP decodes an HTTP infer body against the model signature.
// headerLength is the Inference-Header-Content-Length value, or NoHeaderLength when the header is absent.
// A header length of zero is the raw-binary request.
// Binary tensor data in the result shares memory with body.
func DecodeHTTP(sig Signature, body []byte, headerLength int64) (*Request, error) {
	switch {
	case headerLength == NoHeaderLength:
		return decodeDocument(sig, body, nil, false)
	case headerLength == 0:
		return DecodeRaw(sig, body)
	case headerLength < 0 || headerLength > int64(len(body)):
		return nil, invalidf("Inference-Header-Content-Length is outside the body")
	default:
		return decodeDocument(sig, body[:headerLength], body[headerLength:], true)
	}
}

func decodeDocument(sig Signature, doc, suffix []byte, framed bool) (*Request, error) {
	top, ok := jsonObject(doc)
	if !ok {
		return nil, invalidf("request body must be a JSON object")
	}
	req := &Request{}
	if raw := top["id"]; !absent(raw) {
		if req.ID, ok = jsonString(raw); !ok {
			return nil, invalidf("request id must be a string")
		}
	}
	params, err := jsonParams("request", top["parameters"])
	if err != nil {
		return nil, err
	}
	if req.Parameters, err = ParseRequestParameters(params); err != nil {
		return nil, err
	}
	if req.Inputs, err = decodeInputs(sig, top["inputs"], suffix, framed); err != nil {
		return nil, err
	}
	if req.Outputs, err = decodeOutputs(sig, top["outputs"], req.Parameters); err != nil {
		return nil, err
	}
	return req, nil
}

func decodeInputs(sig Signature, raw json.RawMessage, suffix []byte, framed bool) ([]Tensor, error) {
	items, ok := jsonArray(raw)
	if !ok {
		return nil, invalidf("request inputs must be a JSON array")
	}
	index := specIndex(sig.Inputs)
	out := make([]Tensor, len(sig.Inputs))
	seen := make([]bool, len(sig.Inputs))
	var offset int64
	for _, item := range items {
		obj, ok := jsonObject(item)
		if !ok {
			return nil, invalidf("each request input must be a JSON object")
		}
		name, ok := jsonString(obj["name"])
		if !ok {
			return nil, invalidf("input name must be a string")
		}
		i, ok := index[name]
		if !ok {
			return nil, invalidf("unknown input %q", name)
		}
		if seen[i] {
			return nil, invalidf("duplicate input %q", name)
		}
		seen[i] = true
		spec := sig.Inputs[i]

		t, count, err := decodeInputHeader(spec, obj, sig.MaxBatchSize)
		if err != nil {
			return nil, err
		}
		params, err := jsonParams("input "+strconv.Quote(name), obj["parameters"])
		if err != nil {
			return nil, err
		}
		ip, err := ParseInputParameters(name, params)
		if err != nil {
			return nil, err
		}
		hasData := !absent(obj["data"])
		switch {
		case ip.Binary && hasData:
			return nil, invalidf("input %q has both data and binary_data_size", name)
		case ip.Binary:
			if !framed {
				return nil, invalidf("input %q has binary_data_size and the request has no Inference-Header-Content-Length", name)
			}
			size := ip.BinaryDataSize
			if spec.Datatype != TypeBytes {
				want, err := fixedByteSize(spec.Datatype, count)
				if err != nil {
					return nil, err
				}
				if size != want {
					return nil, invalidf("input %q binary_data_size is %d and its shape needs %d", name, size, want)
				}
			}
			if size > int64(len(suffix))-offset {
				return nil, invalidf("binary data for input %q is shorter than its binary_data_size", name)
			}
			t.Data = suffix[offset : offset+size : offset+size]
			offset += size
			if err := CheckData(t.Datatype, t.Shape, t.Data); err != nil {
				return nil, invalidf("input %q: %v", name, err)
			}
		case hasData:
			if t.Data, err = decodeJSONData(name, t.Datatype, t.Shape, count, obj["data"]); err != nil {
				return nil, err
			}
		default:
			return nil, invalidf("input %q has neither data nor binary_data_size", name)
		}
		out[i] = t
	}
	if offset != int64(len(suffix)) {
		return nil, invalidf("binary data is %d bytes and the inputs declare %d", len(suffix), offset)
	}
	for i, ok := range seen {
		if !ok {
			return nil, invalidf("missing input %q", sig.Inputs[i].Name)
		}
	}
	return out, nil
}

// decodeInputHeader reads the datatype and shape of one input and checks them against spec.
func decodeInputHeader(spec TensorSpec, obj map[string]json.RawMessage, maxBatchSize int) (Tensor, int64, error) {
	t := Tensor{Name: spec.Name}
	name, ok := jsonString(obj["datatype"])
	if !ok {
		return t, 0, invalidf("input %q datatype must be a string", spec.Name)
	}
	d, err := ParseDatatype(name)
	if err != nil {
		return t, 0, err
	}
	if d != spec.Datatype {
		return t, 0, invalidf("input %q datatype is %s and the model expects %s", spec.Name, d, spec.Datatype)
	}
	t.Datatype = d
	dims, ok := jsonArray(obj["shape"])
	if !ok {
		return t, 0, invalidf("input %q shape must be a JSON array", spec.Name)
	}
	t.Shape = make([]int64, len(dims))
	for i, raw := range dims {
		n, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 64)
		if err != nil {
			return t, 0, invalidf("input %q shape must hold integers", spec.Name)
		}
		t.Shape[i] = n
	}
	if err := checkShape(spec, t.Shape, maxBatchSize); err != nil {
		return t, 0, err
	}
	count, err := ElementCount(t.Shape)
	if err != nil {
		return t, 0, invalidf("input %q: %v", spec.Name, err)
	}
	return t, count, nil
}

func decodeOutputs(sig Signature, raw json.RawMessage, rp RequestParameters) ([]RequestedOutput, error) {
	var items []json.RawMessage
	if !absent(raw) {
		var ok bool
		if items, ok = jsonArray(raw); !ok {
			return nil, invalidf("request outputs must be a JSON array")
		}
	}
	if len(items) == 0 {
		return allOutputs(sig, rp)
	}
	index := specIndex(sig.Outputs)
	seen := make([]bool, len(sig.Outputs))
	out := make([]RequestedOutput, 0, len(items))
	for _, item := range items {
		obj, ok := jsonObject(item)
		if !ok {
			return nil, invalidf("each requested output must be a JSON object")
		}
		name, ok := jsonString(obj["name"])
		if !ok {
			return nil, invalidf("output name must be a string")
		}
		i, ok := index[name]
		if !ok {
			return nil, invalidf("unknown output %q", name)
		}
		if seen[i] {
			return nil, invalidf("duplicate output %q", name)
		}
		seen[i] = true
		params, err := jsonParams("output "+strconv.Quote(name), obj["parameters"])
		if err != nil {
			return nil, err
		}
		op, err := ParseOutputParameters(name, params)
		if err != nil {
			return nil, err
		}
		spec := sig.Outputs[i]
		binary, err := outputBinary(spec, op, rp)
		if err != nil {
			return nil, err
		}
		out = append(out, RequestedOutput{Name: spec.Name, Datatype: spec.Datatype, Binary: binary})
	}
	return out, nil
}

// allOutputs returns every model output in model order.
func allOutputs(sig Signature, rp RequestParameters) ([]RequestedOutput, error) {
	out := make([]RequestedOutput, len(sig.Outputs))
	for i, spec := range sig.Outputs {
		binary, err := outputBinary(spec, OutputParameters{}, rp)
		if err != nil {
			return nil, err
		}
		out[i] = RequestedOutput{Name: spec.Name, Datatype: spec.Datatype, Binary: binary}
	}
	return out, nil
}

// outputBinary resolves how one output is returned. The output's own flag wins over the request flag.
func outputBinary(spec TensorSpec, op OutputParameters, rp RequestParameters) (bool, error) {
	binary, set := op.Binary, op.BinarySet
	if !set {
		binary, set = rp.BinaryOutput, rp.BinaryOutputSet
	}
	if !set {
		return spec.Datatype.BinaryOutputOnly(), nil
	}
	if !binary && spec.Datatype.BinaryOutputOnly() {
		return false, invalidf("%s output %q is returned only as binary data", spec.Datatype, spec.Name)
	}
	return binary, nil
}

func specIndex(specs []TensorSpec) map[string]int {
	m := make(map[string]int, len(specs))
	for i, s := range specs {
		m[s.Name] = i
	}
	return m
}

// jsonWalker reads nested or flat JSON tensor values in row-major order.
type jsonWalker struct {
	dec      *json.Decoder
	name     string
	datatype Datatype
	shape    []int64
	count    int64
	seen     int64
	nested   bool
	flat     bool
	out      []byte
}

func decodeJSONData(name string, d Datatype, shape []int64, count int64, raw json.RawMessage) ([]byte, error) {
	if !d.JSONInput() {
		return nil, invalidf("%s input %q must be sent as binary data", d, name)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, invalidf("input %q data must be a JSON array", name)
	}
	// Each JSON value takes at least two bytes with its separator, so the body bounds the first allocation.
	capHint := min(count, int64(len(raw)/2+1))
	if size := d.ElementSize(); size > 0 {
		capHint *= int64(size)
	}
	w := &jsonWalker{dec: dec, name: name, datatype: d, shape: shape, count: count, out: make([]byte, 0, capHint)}
	if err := w.array(0); err != nil {
		return nil, err
	}
	if w.seen != count {
		return nil, invalidf("input %q data has %d values and the shape needs %d", name, w.seen, count)
	}
	return w.out, nil
}

// array reads the values of one JSON array whose opening bracket was already read.
func (w *jsonWalker) array(depth int) error {
	var length int64
	for w.dec.More() {
		tok, err := w.dec.Token()
		if err != nil {
			return invalidf("input %q data is not valid JSON", w.name)
		}
		if delim, ok := tok.(json.Delim); ok {
			if delim != '[' {
				return invalidf("input %q data must hold only arrays and values", w.name)
			}
			if w.flat || depth+1 >= len(w.shape) {
				return invalidf("input %q data nesting does not match the shape rank %d", w.name, len(w.shape))
			}
			w.nested = true
			if err := w.array(depth + 1); err != nil {
				return err
			}
		} else {
			if !w.nested && depth == 0 {
				w.flat = true
			} else if depth != len(w.shape)-1 {
				return invalidf("input %q data nesting does not match the shape rank %d", w.name, len(w.shape))
			}
			if w.seen >= w.count {
				return invalidf("input %q data has more values than the shape needs", w.name)
			}
			if w.out, err = appendJSONValue(w.out, w.datatype, tok); err != nil {
				return invalidf("input %q value %d: %v", w.name, w.seen, err)
			}
			w.seen++
		}
		length++
	}
	if tok, err := w.dec.Token(); err != nil || tok != json.Delim(']') {
		return invalidf("input %q data is not valid JSON", w.name)
	}
	if w.nested && length != w.shape[depth] {
		return invalidf("input %q data dimension %d has %d values and the shape needs %d", w.name, depth, length, w.shape[depth])
	}
	return nil
}

var errNotExact = errors.New("integer is not exact in JSON; send it as binary data")

// integerRanges are the JSON integer bounds of each integer datatype.
var integerRanges = map[Datatype][2]int64{
	TypeInt8:   {math.MinInt8, math.MaxInt8},
	TypeInt16:  {math.MinInt16, math.MaxInt16},
	TypeInt32:  {math.MinInt32, math.MaxInt32},
	TypeInt64:  {-maxExactJSONInteger, maxExactJSONInteger},
	TypeUint8:  {0, math.MaxUint8},
	TypeUint16: {0, math.MaxUint16},
	TypeUint32: {0, math.MaxUint32},
	TypeUint64: {0, maxExactJSONInteger},
}

func appendJSONValue(out []byte, d Datatype, tok json.Token) ([]byte, error) {
	switch d {
	case TypeBool:
		b, ok := tok.(bool)
		if !ok {
			return out, errors.New("BOOL must be true or false")
		}
		if b {
			return append(out, 1), nil
		}
		return append(out, 0), nil
	case TypeBytes:
		s, ok := tok.(string)
		if !ok {
			return out, errors.New("BYTES must be a string")
		}
		if uint64(len(s)) > math.MaxUint32 {
			return out, errors.New("BYTES element is too long")
		}
		out = appendUint(out, uint64(len(s)), bytesLengthSize)
		return append(out, s...), nil
	case TypeFP32, TypeFP64:
		num, ok := tok.(json.Number)
		if !ok {
			return out, errors.New("value must be a number")
		}
		if d == TypeFP32 {
			f, err := strconv.ParseFloat(string(num), 32)
			if err != nil {
				return out, errors.New("number does not fit FP32")
			}
			return appendUint(out, uint64(math.Float32bits(float32(f))), 4), nil
		}
		f, err := strconv.ParseFloat(string(num), 64)
		if err != nil {
			return out, errors.New("number does not fit FP64")
		}
		return appendUint(out, math.Float64bits(f), 8), nil
	default:
	}
	bounds, ok := integerRanges[d]
	if !ok {
		return out, errors.New("datatype has no JSON form")
	}
	num, ok := tok.(json.Number)
	if !ok {
		return out, errors.New("value must be an integer")
	}
	n, err := strconv.ParseInt(string(num), 10, 64)
	switch {
	case errors.Is(err, strconv.ErrRange):
		return out, errNotExact
	case err != nil:
		return out, errors.New("value must be an integer")
	case n > maxExactJSONInteger || n < -maxExactJSONInteger:
		return out, errNotExact
	case n < bounds[0] || n > bounds[1]:
		return out, errors.New("integer does not fit " + string(d))
	default:
	}
	return appendUint(out, uint64(n), d.ElementSize()), nil // #nosec G115 -- range checked above; two's-complement bits of the value
}

// appendUint appends the low size bytes of v in little-endian order.
func appendUint(out []byte, v uint64, size int) []byte {
	for i := range size {
		out = append(out, byte(v>>(8*i))) // #nosec G115 -- keeps one byte of v on purpose
	}
	return out
}

func jsonObject(raw []byte) (map[string]json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil, false
	}
	return m, true
}

func jsonArray(raw []byte) ([]json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var a []json.RawMessage
	if json.Unmarshal(raw, &a) != nil {
		return nil, false
	}
	return a, true
}

func jsonString(raw []byte) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}
