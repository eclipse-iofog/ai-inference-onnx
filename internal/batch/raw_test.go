package batch

import (
	"bytes"
	"slices"
	"testing"
)

func TestCheckRaw(t *testing.T) {
	batched := func(s Signature, n int) Signature {
		s.MaxBatchSize = n
		return s
	}
	cases := []struct {
		name     string
		sig      Signature
		fragment string
	}{
		{"add model", addSignature(), "has 2"},
		{"no inputs", Signature{}, "has 0"},
		{"fixed shape", oneInput(TypeFP32, 2, 3), ""},
		{"one variable dimension", oneInput(TypeFP32, -1, 3), ""},
		{"two variable dimensions", oneInput(TypeFP32, -1, -1), "at most one variable dimension"},
		{"batched with one more variable dimension", batched(oneInput(TypeFP32, -1, -1, 3), 8), ""},
		{"batched with two more variable dimensions", batched(oneInput(TypeFP32, -1, -1, -1), 8), "at most one variable dimension"},
		{"bytes [1]", oneInput(TypeBytes, 1), ""},
		{"bytes batched [1]", batched(oneInput(TypeBytes, -1, 1), 4), ""},
		{"bytes [2]", oneInput(TypeBytes, 2), "needs shape [1]"},
		{"bytes variable", oneInput(TypeBytes, -1), "needs shape [1]"},
		{"bytes [1, 1] unbatched", oneInput(TypeBytes, 1, 1), "needs shape [1]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckRaw(c.sig)
			if c.fragment == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			wantInvalid(t, err, c.fragment)
		})
	}
}

func TestDecodeRawDeducesShapeAsBatchOne(t *testing.T) {
	sig := Signature{
		Inputs: []TensorSpec{{Name: "x", Datatype: TypeFP32, Shape: []int64{-1, -1, 2}}},
		Outputs: []TensorSpec{
			{Name: "y", Datatype: TypeFP32, Shape: []int64{-1, 2}},
			{Name: "z", Datatype: TypeInt32, Shape: []int64{-1}},
		},
		MaxBatchSize: 8,
	}
	body := fp32Bytes(1, 2, 3, 4, 5, 6)
	req, err := DecodeHTTP(sig, body, 0)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in := req.Inputs[0]
	if !slices.Equal(in.Shape, []int64{1, 3, 2}) || !bytes.Equal(in.Data, body) {
		t.Fatalf("input %+v", in)
	}
	if &in.Data[0] != &body[0] {
		t.Fatal("raw tensor data was copied")
	}
	if len(req.Outputs) != 2 || req.Outputs[0].Name != "y" || req.Outputs[1].Name != "z" {
		t.Fatalf("outputs %+v", req.Outputs)
	}
	for _, o := range req.Outputs {
		if !o.Binary {
			t.Fatalf("output %s is not binary", o.Name)
		}
	}
	if req.ID != "" || req.Parameters != (RequestParameters{}) {
		t.Fatalf("raw request carries id or parameters: %+v", req)
	}
}

func TestDecodeRawUnbatched(t *testing.T) {
	req, err := DecodeRaw(oneInput(TypeFP32, 2, -1), fp32Bytes(1, 2, 3, 4))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !slices.Equal(req.Inputs[0].Shape, []int64{2, 2}) {
		t.Fatalf("shape %v", req.Inputs[0].Shape)
	}
	req, err = DecodeRaw(oneInput(TypeFP32, 2, 3), fp32Bytes(1, 2, 3, 4, 5, 6))
	if err != nil || !slices.Equal(req.Inputs[0].Shape, []int64{2, 3}) {
		t.Fatalf("fixed shape: %v", err)
	}
}

func TestDecodeRawBytes(t *testing.T) {
	data := bytesElements("hello")
	sig := oneInput(TypeBytes, -1, 1)
	sig.MaxBatchSize = 2
	req, err := DecodeRaw(sig, data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !slices.Equal(req.Inputs[0].Shape, []int64{1, 1}) {
		t.Fatalf("shape %v", req.Inputs[0].Shape)
	}
	_, err = DecodeRaw(oneInput(TypeBytes, 1), append(bytesElements("a"), bytesElements("b")...))
	wantInvalid(t, err, "has 2 elements and the shape needs 1")
}

func TestDecodeRawRejects(t *testing.T) {
	_, err := DecodeHTTP(addSignature(), fp32Bytes(1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2), 0)
	wantInvalid(t, err, "one input")
	_, err = DecodeRaw(oneInput(TypeFP32, -1, 3), fp32Bytes(1, 2, 3, 4))
	wantInvalid(t, err, "does not fit the shape")
	_, err = DecodeRaw(oneInput(TypeFP32, 2, 3), fp32Bytes(1, 2, 3))
	wantInvalid(t, err, "the shape needs 24")
	_, err = DecodeRaw(oneInput(TypeBool, -1), []byte{1, 0, 3})
	wantInvalid(t, err, "byte other than 0 or 1")
	_, err = DecodeRaw(oneInput(TypeFP32, -1, 0), nil)
	wantInvalid(t, err, "does not fit the shape")
}
