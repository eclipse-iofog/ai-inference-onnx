package batch

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
)

// addSignature matches the committed add model: two FP32 inputs of shape [2, 3] and one output.
func addSignature() Signature {
	return Signature{
		Inputs: []TensorSpec{
			{Name: "A", Datatype: TypeFP32, Shape: []int64{2, 3}},
			{Name: "B", Datatype: TypeFP32, Shape: []int64{2, 3}},
		},
		Outputs: []TensorSpec{{Name: "C", Datatype: TypeFP32, Shape: []int64{2, 3}}},
	}
}

// oneInput is a signature with a single input and a single FP32 output.
func oneInput(d Datatype, shape ...int64) Signature {
	return Signature{
		Inputs:  []TensorSpec{{Name: "x", Datatype: d, Shape: shape}},
		Outputs: []TensorSpec{{Name: "y", Datatype: TypeFP32, Shape: []int64{-1}}},
	}
}

func fp32Bytes(vals ...float32) []byte {
	out := make([]byte, 0, 4*len(vals))
	for _, v := range vals {
		out = binary.LittleEndian.AppendUint32(out, math.Float32bits(v))
	}
	return out
}

func bytesElements(vals ...string) []byte {
	var out []byte
	for _, v := range vals {
		out = binary.LittleEndian.AppendUint32(out, uint32(len(v)))
		out = append(out, v...)
	}
	return out
}

func mustDecode(t *testing.T, sig Signature, body string) *Request {
	t.Helper()
	req, err := DecodeHTTP(sig, []byte(body), NoHeaderLength)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return req
}

func wantInvalid(t *testing.T, err error, fragment string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q", fragment)
	}
	var inv *InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("error %v is not an InvalidError", err)
	}
	if !strings.Contains(err.Error(), fragment) {
		t.Fatalf("error %q does not contain %q", err, fragment)
	}
}

// framed joins a JSON header and a binary suffix and returns the header length.
func framed(header string, suffix ...[]byte) ([]byte, int64) {
	body := []byte(header)
	for _, s := range suffix {
		body = append(body, s...)
	}
	return body, int64(len(header))
}
