package batch

import "slices"

// CheckRaw reports whether a model with this signature accepts a raw-binary body, one that carries tensor bytes and no JSON.
// The model must have one input. A BYTES input must have shape [1] after the batch dimension.
// Any other input may have at most one variable dimension after the batch dimension, so the byte count fixes the shape.
func CheckRaw(sig Signature) error {
	if len(sig.Inputs) != 1 {
		return invalidf("a raw binary body needs a model with one input, and this model has %d", len(sig.Inputs))
	}
	spec := sig.Inputs[0]
	if !spec.Datatype.Known() {
		return invalidf("unknown datatype %q", spec.Datatype)
	}
	tail := spec.Shape
	if sig.MaxBatchSize > 0 {
		if len(tail) == 0 {
			return invalidf("input %q has no batch dimension", spec.Name)
		}
		tail = tail[1:]
	}
	if spec.Datatype == TypeBytes {
		if !slices.Equal(tail, []int64{1}) {
			return invalidf("a raw binary body for BYTES input %q needs shape [1]", spec.Name)
		}
		return nil
	}
	variable := 0
	for _, d := range tail {
		if d < 0 {
			variable++
		}
	}
	if variable > 1 {
		return invalidf("a raw binary body for input %q allows at most one variable dimension, and it has %d", spec.Name, variable)
	}
	return nil
}

// DecodeRaw decodes a raw-binary body as batch 1, deducing a variable dimension from the byte count.
// Every model output is returned as binary data. The tensor data shares memory with body.
func DecodeRaw(sig Signature, body []byte) (*Request, error) {
	if err := CheckRaw(sig); err != nil {
		return nil, err
	}
	spec := sig.Inputs[0]
	shape := slices.Clone(spec.Shape)
	start := 0
	if sig.MaxBatchSize > 0 {
		shape[0] = 1
		start = 1
	}
	variable := -1
	known := int64(1)
	for i := start; i < len(shape); i++ {
		if shape[i] < 0 {
			variable = i
			continue
		}
		var err error
		if known, err = ElementCount([]int64{known, shape[i]}); err != nil {
			return nil, err
		}
	}
	if variable >= 0 {
		unit, err := fixedByteSize(spec.Datatype, known)
		if err != nil {
			return nil, err
		}
		if unit == 0 || int64(len(body))%unit != 0 {
			return nil, invalidf("raw binary body of %d bytes does not fit the shape of input %q", len(body), spec.Name)
		}
		shape[variable] = int64(len(body)) / unit
	}
	if err := CheckData(spec.Datatype, shape, body); err != nil {
		return nil, invalidf("input %q: %v", spec.Name, err)
	}
	outputs := make([]RequestedOutput, len(sig.Outputs))
	for i, o := range sig.Outputs {
		outputs[i] = RequestedOutput{Name: o.Name, Datatype: o.Datatype, Binary: true}
	}
	return &Request{
		Inputs:  []Tensor{{Name: spec.Name, Datatype: spec.Datatype, Shape: shape, Data: body[:len(body):len(body)]}},
		Outputs: outputs,
	}, nil
}
