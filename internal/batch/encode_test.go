package batch

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

func TestEncodeJSONOutputs(t *testing.T) {
	le := binary.LittleEndian
	enc, err := EncodeHTTP(Response{
		ModelName:    "add",
		ModelVersion: "1",
		ID:           "r1",
		Outputs: []ResponseOutput{
			{Tensor: Tensor{Name: "C", Datatype: TypeFP32, Shape: []int64{2, 3}, Data: fp32Bytes(3, 3, 3, 3, 3, 0.5)}},
			{Tensor: Tensor{Name: "b", Datatype: TypeBool, Shape: []int64{2}, Data: []byte{1, 0}}},
			{Tensor: Tensor{Name: "s", Datatype: TypeBytes, Shape: []int64{2}, Data: bytesElements("hi", "")}},
			{Tensor: Tensor{Name: "i8", Datatype: TypeInt8, Shape: []int64{2}, Data: []byte{0xff, 5}}},
			{Tensor: Tensor{Name: "u8", Datatype: TypeUint8, Shape: []int64{2}, Data: []byte{0xff, 5}}},
			{Tensor: Tensor{Name: "i16", Datatype: TypeInt16, Shape: []int64{1}, Data: le.AppendUint16(nil, 0xfffe)}},
			{Tensor: Tensor{Name: "u32", Datatype: TypeUint32, Shape: []int64{1}, Data: le.AppendUint32(nil, math.MaxUint32)}},
			{Tensor: Tensor{Name: "f64", Datatype: TypeFP64, Shape: []int64{1}, Data: le.AppendUint64(nil, math.Float64bits(-1.25))}},
			{Tensor: Tensor{Name: "empty", Datatype: TypeFP32, Shape: []int64{0}, Data: nil}},
		},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if enc.Binary || enc.HeaderLength != len(enc.Body) {
		t.Fatalf("JSON-only response is marked binary: %+v", enc)
	}
	want := `{"model_name":"add","model_version":"1","id":"r1","outputs":[` +
		`{"name":"C","datatype":"FP32","shape":[2,3],"data":[3,3,3,3,3,0.5]},` +
		`{"name":"b","datatype":"BOOL","shape":[2],"data":[true,false]},` +
		`{"name":"s","datatype":"BYTES","shape":[2],"data":["hi",""]},` +
		`{"name":"i8","datatype":"INT8","shape":[2],"data":[-1,5]},` +
		`{"name":"u8","datatype":"UINT8","shape":[2],"data":[255,5]},` +
		`{"name":"i16","datatype":"INT16","shape":[1],"data":[-2]},` +
		`{"name":"u32","datatype":"UINT32","shape":[1],"data":[4294967295]},` +
		`{"name":"f64","datatype":"FP64","shape":[1],"data":[-1.25]},` +
		`{"name":"empty","datatype":"FP32","shape":[0],"data":[]}]}`
	if string(enc.Body) != want {
		t.Fatalf("body\n%s\nwant\n%s", enc.Body, want)
	}
}

func TestEncodeEmptyIDAndVersionArePresent(t *testing.T) {
	enc, err := EncodeHTTP(Response{ModelName: "m", ModelVersion: "1"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(enc.Body) != `{"model_name":"m","model_version":"1","id":"","outputs":[]}` {
		t.Fatalf("body %s", enc.Body)
	}
}

func TestEncodeBinaryOutputs(t *testing.T) {
	c := fp32Bytes(3, 3, 3, 3, 3, 3)
	d := []byte{7, 0, 0, 0, 0, 0, 0, 0}
	enc, err := EncodeHTTP(Response{
		ModelName:    "add",
		ModelVersion: "1",
		Outputs: []ResponseOutput{
			{Tensor: Tensor{Name: "C", Datatype: TypeFP32, Shape: []int64{2, 3}, Data: c}, Binary: true},
			{Tensor: Tensor{Name: "j", Datatype: TypeFP32, Shape: []int64{1}, Data: fp32Bytes(1)}},
			{Tensor: Tensor{Name: "D", Datatype: TypeInt64, Shape: []int64{1}, Data: d}, Binary: true},
		},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !enc.Binary {
		t.Fatal("response is not marked binary")
	}
	header := enc.Body[:enc.HeaderLength]
	if !bytes.Equal(enc.Body[enc.HeaderLength:], append(append([]byte{}, c...), d...)) {
		t.Fatalf("suffix %v", enc.Body[enc.HeaderLength:])
	}
	type decodedOutput struct {
		Name       string          `json:"name"`
		Parameters map[string]int  `json:"parameters"`
		Data       json.RawMessage `json:"data"`
	}
	var doc struct {
		Outputs []decodedOutput `json:"outputs"`
	}
	if err := json.Unmarshal(header, &doc); err != nil {
		t.Fatalf("header is not JSON: %v", err)
	}
	if doc.Outputs[0].Parameters["binary_data_size"] != 24 || doc.Outputs[0].Data != nil {
		t.Fatalf("output C %+v", doc.Outputs[0])
	}
	if doc.Outputs[1].Parameters != nil || string(doc.Outputs[1].Data) != "[1]" {
		t.Fatalf("output j %+v", doc.Outputs[1])
	}
	if doc.Outputs[2].Parameters["binary_data_size"] != 8 {
		t.Fatalf("output D %+v", doc.Outputs[2])
	}
}

func TestBinaryOnlyOutputTypes(t *testing.T) {
	for _, d := range []Datatype{TypeInt64, TypeUint64, TypeFP16, TypeBF16} {
		t.Run(string(d), func(t *testing.T) {
			sig := Signature{
				Inputs:  []TensorSpec{{Name: "x", Datatype: TypeFP32, Shape: []int64{1}}},
				Outputs: []TensorSpec{{Name: "y", Datatype: d, Shape: []int64{1}}},
			}
			req := mustDecode(t, sig, fmt.Sprintf(`{"inputs":[%s]}`, oneFP32))
			if !req.Outputs[0].Binary {
				t.Fatalf("%s output defaults to JSON", d)
			}
			req = mustDecode(t, sig, fmt.Sprintf(`{"inputs":[%s],"outputs":[{"name":"y"}]}`, oneFP32))
			if !req.Outputs[0].Binary {
				t.Fatalf("named %s output defaults to JSON", d)
			}

			_, err := DecodeHTTP(sig, fmt.Appendf(nil, `{"inputs":[%s],"outputs":[{"name":"y","parameters":{"binary_data":false}}]}`, oneFP32), NoHeaderLength)
			wantInvalid(t, err, "returned only as binary data")
			_, err = DecodeHTTP(sig, fmt.Appendf(nil, `{"parameters":{"binary_data_output":false},"inputs":[%s]}`, oneFP32), NoHeaderLength)
			wantInvalid(t, err, "returned only as binary data")

			_, err = EncodeHTTP(Response{Outputs: []ResponseOutput{{Tensor: Tensor{Name: "y", Datatype: d, Shape: []int64{1}, Data: make([]byte, d.ElementSize())}}}})
			wantInvalid(t, err, "returned only as binary data")
			enc, err := EncodeHTTP(Response{Outputs: []ResponseOutput{{Tensor: Tensor{Name: "y", Datatype: d, Shape: []int64{1}, Data: make([]byte, d.ElementSize())}, Binary: true}}})
			if err != nil || !enc.Binary {
				t.Fatalf("binary %s output: %v", d, err)
			}
		})
	}
}

func TestEncodeRejectsBadOutputs(t *testing.T) {
	cases := []struct {
		name string
		out  Tensor
	}{
		{"size mismatch", Tensor{Name: "y", Datatype: TypeFP32, Shape: []int64{2}, Data: fp32Bytes(1)}},
		{"bool byte", Tensor{Name: "y", Datatype: TypeBool, Shape: []int64{1}, Data: []byte{9}}},
		{"bytes overrun", Tensor{Name: "y", Datatype: TypeBytes, Shape: []int64{1}, Data: []byte{9, 0, 0, 0, 'a'}}},
		{"unknown datatype", Tensor{Name: "y", Datatype: "FP8", Shape: []int64{1}, Data: []byte{1}}},
		{"not a JSON number", Tensor{Name: "y", Datatype: TypeFP32, Shape: []int64{1}, Data: fp32Bytes(float32(math.NaN()))}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := EncodeHTTP(Response{Outputs: []ResponseOutput{{Tensor: c.out}}}); err == nil {
				t.Fatal("bad output was encoded")
			}
		})
	}
}

func TestJSONRoundTrip(t *testing.T) {
	sig := oneInput(TypeInt32, 2, 2)
	req := mustDecode(t, sig, `{"inputs":[{"name":"x","shape":[2,2],"datatype":"INT32","data":[[-1,2],[3,-4]]}]}`)
	in := req.Inputs[0]
	enc, err := EncodeHTTP(Response{ModelName: "m", ModelVersion: "1", Outputs: []ResponseOutput{{Tensor: in}}})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.Contains(enc.Body, []byte(`"shape":[2,2],"data":[-1,2,3,-4]`)) {
		t.Fatalf("body %s", enc.Body)
	}
}
