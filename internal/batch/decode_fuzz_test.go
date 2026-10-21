package batch

import (
	"errors"
	"fmt"
	"testing"
)

// FuzzDecodeTensor checks that a request body is decoded or refused without crashing.
func FuzzDecodeTensor(f *testing.F) {
	valid := []byte(`{"id":"r1","inputs":[
		{"name":"B","shape":[2,3],"datatype":"FP32","data":[[1,2,3],[4,5,6]]},
		{"name":"A","shape":[2,3],"datatype":"FP32","data":[[1,2,3],[4,5,6]]}]}`)
	f.Add(valid, NoHeaderLength, 0)

	header := `{"inputs":[
		{"name":"A","shape":[2,3],"datatype":"FP32","parameters":{"binary_data_size":24}},
		{"name":"B","shape":[2,3],"datatype":"FP32","parameters":{"binary_data_size":24}}],
		"outputs":[{"name":"C","parameters":{"binary_data":true}}]}`
	binaryBody, binaryLen := framed(header, fp32Bytes(1, 1, 1, 1, 1, 1), fp32Bytes(2, 2, 2, 2, 2, 2))
	f.Add(binaryBody, binaryLen, 0)

	truncatedHeader := `{"inputs":[
		{"name":"A","shape":[2,3],"datatype":"FP32","parameters":{"binary_data_size":24}},
		{"name":"B","shape":[2,3],"datatype":"FP32","parameters":{"binary_data_size":24}}]}`
	truncated, truncatedLen := framed(truncatedHeader, append(fp32Bytes(1, 2, 3, 4, 5, 6), fp32Bytes(1, 2, 3, 4, 5)...))
	f.Add(truncated, truncatedLen, 0)

	f.Add([]byte(`{"inputs":[{"name":"x","shape":[1],"datatype":"FP16","data":[1.0]}]}`), NoHeaderLength, 1)
	f.Add([]byte(`{"inputs":[{"name":"x","shape":[1],"datatype":"INT64","data":[9007199254740993]}]}`), NoHeaderLength, 2)
	f.Add(fp32Bytes(1, 2, 3, 4, 5, 6), int64(0), 3)

	f.Fuzz(func(t *testing.T, body []byte, headerLength int64, which int) {
		sigs := fuzzSignatures()
		sig := sigs[int(uint(which)%uint(len(sigs)))]
		req, err := DecodeHTTP(sig, body, headerLength)
		if err != nil {
			var inv *InvalidError
			if !errors.As(err, &inv) {
				t.Fatalf("decode: %v", err)
			}
			return
		}
		if err := fuzzDecodedRequest(sig, req); err != nil {
			t.Fatal(err)
		}
	})
}

// fuzzSignatures are model shapes a request body is decoded against.
// Index 0 is the two-input model, 1 is FP16, 2 is INT64, and 3 accepts a raw body.
func fuzzSignatures() []Signature {
	return []Signature{
		addSignature(),
		oneInput(TypeFP16, 1),
		oneInput(TypeInt64, 1),
		{
			Inputs: []TensorSpec{{Name: "x", Datatype: TypeFP32, Shape: []int64{-1, -1, 2}}},
			Outputs: []TensorSpec{
				{Name: "y", Datatype: TypeFP32, Shape: []int64{-1, 2}},
				{Name: "z", Datatype: TypeInt32, Shape: []int64{-1}},
			},
			MaxBatchSize: 8,
		},
		oneInput(TypeFP32, 1),
		oneInput(TypeBool, 3),
		oneInput(TypeBytes, 3),
		oneInput(TypeBF16, 1),
		oneInput(TypeUint64, 1),
		oneInput(TypeInt32, 1),
		oneInput(TypeFP32, 2, -1),
		oneInput(TypeBytes, 1),
	}
}

func fuzzDecodedRequest(sig Signature, req *Request) error {
	if req == nil {
		return errors.New("decoded request is missing")
	}
	if len(req.Inputs) != len(sig.Inputs) {
		return fmt.Errorf("decoded %d inputs and the model has %d", len(req.Inputs), len(sig.Inputs))
	}
	for i, in := range req.Inputs {
		spec := sig.Inputs[i]
		if in.Name != spec.Name || in.Datatype != spec.Datatype {
			return fmt.Errorf("input %d is %s %s", i, in.Name, in.Datatype)
		}
		if len(in.Shape) != len(spec.Shape) {
			return fmt.Errorf("input %s has rank %d", in.Name, len(in.Shape))
		}
		for d, dim := range in.Shape {
			if dim < 0 {
				return fmt.Errorf("input %s has a negative dimension", in.Name)
			}
			if d == 0 && sig.MaxBatchSize > 0 {
				continue
			}
			if want := spec.Shape[d]; want >= 0 && dim != want {
				return fmt.Errorf("input %s dimension %d is %d", in.Name, d, dim)
			}
		}
		if err := CheckData(in.Datatype, in.Shape, in.Data); err != nil {
			return fmt.Errorf("input %s: %w", in.Name, err)
		}
	}
	for _, out := range req.Outputs {
		found := false
		for _, spec := range sig.Outputs {
			if spec.Name == out.Name && spec.Datatype == out.Datatype {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("output %s is not in the model", out.Name)
		}
	}
	return nil
}
