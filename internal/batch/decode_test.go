package batch

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"slices"
	"testing"
)

func TestDecodeJSONNestedAndFlatFP32(t *testing.T) {
	want := fp32Bytes(1, 2, 3, 4, 5, 6)
	for name, data := range map[string]string{
		"nested": `[[1, 2, 3], [4, 5, 6]]`,
		"flat":   `[1, 2, 3, 4, 5, 6.0]`,
	} {
		t.Run(name, func(t *testing.T) {
			body := fmt.Sprintf(`{"id":"r1","inputs":[
				{"name":"B","shape":[2,3],"datatype":"FP32","data":%s},
				{"name":"A","shape":[2,3],"datatype":"FP32","data":%s}]}`, data, data)
			req := mustDecode(t, addSignature(), body)
			if req.ID != "r1" {
				t.Fatalf("id %q", req.ID)
			}
			if len(req.Inputs) != 2 || req.Inputs[0].Name != "A" || req.Inputs[1].Name != "B" {
				t.Fatalf("inputs are not in model order: %+v", req.Inputs)
			}
			for _, in := range req.Inputs {
				if !bytes.Equal(in.Data, want) || !slices.Equal(in.Shape, []int64{2, 3}) || in.Datatype != TypeFP32 {
					t.Fatalf("input %s: %+v", in.Name, in)
				}
			}
			if len(req.Outputs) != 1 || req.Outputs[0].Name != "C" || req.Outputs[0].Binary {
				t.Fatalf("outputs %+v", req.Outputs)
			}
		})
	}
}

func TestDecodeJSONDatatypes(t *testing.T) {
	le := binary.LittleEndian
	cases := []struct {
		datatype Datatype
		data     string
		want     []byte
	}{
		{TypeBool, `[true, false, true]`, []byte{1, 0, 1}},
		{TypeInt8, `[-128, 0, 127]`, []byte{0x80, 0, 0x7f}},
		{TypeUint8, `[0, 1, 255]`, []byte{0, 1, 255}},
		{TypeInt16, `[-2, 0, 300]`, le.AppendUint16(le.AppendUint16(le.AppendUint16(nil, 0xfffe), 0), 300)},
		{TypeUint16, `[0, 1, 65535]`, le.AppendUint16(le.AppendUint16(le.AppendUint16(nil, 0), 1), 65535)},
		{TypeInt32, `[-1, 0, 2147483647]`, le.AppendUint32(le.AppendUint32(le.AppendUint32(nil, 0xffffffff), 0), math.MaxInt32)},
		{TypeUint32, `[0, 7, 4294967295]`, le.AppendUint32(le.AppendUint32(le.AppendUint32(nil, 0), 7), math.MaxUint32)},
		{TypeInt64, `[-9007199254740992, 0, 9007199254740992]`, le.AppendUint64(le.AppendUint64(le.AppendUint64(nil, uint64(1<<64-1<<53)), 0), 1<<53)},
		{TypeUint64, `[0, 1, 9007199254740992]`, le.AppendUint64(le.AppendUint64(le.AppendUint64(nil, 0), 1), 1<<53)},
		{TypeFP32, `[1.5, -2, 0]`, fp32Bytes(1.5, -2, 0)},
		{TypeFP64, `[1.5, -2, 1e300]`, le.AppendUint64(le.AppendUint64(le.AppendUint64(nil, math.Float64bits(1.5)), math.Float64bits(-2)), math.Float64bits(1e300))},
		{TypeBytes, `["a", "", "héllo"]`, bytesElements("a", "", "héllo")},
	}
	for _, c := range cases {
		t.Run(string(c.datatype), func(t *testing.T) {
			body := fmt.Sprintf(`{"inputs":[{"name":"x","shape":[3],"datatype":%q,"data":%s}]}`, c.datatype, c.data)
			req := mustDecode(t, oneInput(c.datatype, 3), body)
			if !bytes.Equal(req.Inputs[0].Data, c.want) {
				t.Fatalf("data %v, want %v", req.Inputs[0].Data, c.want)
			}
		})
	}
}

func TestDecodeJSONNestedBool(t *testing.T) {
	sig := oneInput(TypeBool, 2, 2)
	req := mustDecode(t, sig, `{"inputs":[{"name":"x","shape":[2,2],"datatype":"BOOL","data":[[true,false],[false,true]]}]}`)
	if !bytes.Equal(req.Inputs[0].Data, []byte{1, 0, 0, 1}) {
		t.Fatalf("data %v", req.Inputs[0].Data)
	}
}

func TestDecodeBatchDimensionIsTheClients(t *testing.T) {
	sig := Signature{
		Inputs:       []TensorSpec{{Name: "x", Datatype: TypeFP32, Shape: []int64{-1, 2}}},
		Outputs:      []TensorSpec{{Name: "y", Datatype: TypeFP32, Shape: []int64{-1, 2}}},
		MaxBatchSize: 4,
	}
	req := mustDecode(t, sig, `{"inputs":[{"name":"x","shape":[3,2],"datatype":"FP32","data":[1,2,3,4,5,6]}]}`)
	if !slices.Equal(req.Inputs[0].Shape, []int64{3, 2}) {
		t.Fatalf("shape %v", req.Inputs[0].Shape)
	}
	fixed := sig
	fixed.Inputs = []TensorSpec{{Name: "x", Datatype: TypeFP32, Shape: []int64{1, 2}}}
	if _, err := DecodeHTTP(fixed, []byte(`{"inputs":[{"name":"x","shape":[3,2],"datatype":"FP32","data":[1,2,3,4,5,6]}]}`), NoHeaderLength); err != nil {
		t.Fatalf("dimension 0 of a batching model was compared: %v", err)
	}
}

func TestDecodeJSONRejects(t *testing.T) {
	input := func(datatype, shape, data string) string {
		return fmt.Sprintf(`{"inputs":[{"name":"x","shape":%s,"datatype":%q,"data":%s}]}`, shape, datatype, data)
	}
	cases := []struct {
		name     string
		sig      Signature
		body     string
		fragment string
	}{
		{"bool as one", oneInput(TypeBool, 2), input("BOOL", "[2]", "[1, 0]"), "BOOL must be true or false"},
		{"bool as zero", oneInput(TypeBool, 1), input("BOOL", "[1]", "[0]"), "BOOL must be true or false"},
		{"integer above 2^53", oneInput(TypeInt64, 1), input("INT64", "[1]", "[9007199254740993]"), "not exact"},
		{"integer below -2^53", oneInput(TypeInt64, 1), input("INT64", "[1]", "[-9007199254740993]"), "not exact"},
		{"integer above int64", oneInput(TypeUint64, 1), input("UINT64", "[1]", "[18446744073709551615]"), "not exact"},
		{"fractional integer", oneInput(TypeInt32, 1), input("INT32", "[1]", "[1.5]"), "must be an integer"},
		{"exponent integer", oneInput(TypeInt32, 1), input("INT32", "[1]", "[1e3]"), "must be an integer"},
		{"string integer", oneInput(TypeInt32, 1), input("INT32", "[1]", `["1"]`), "must be an integer"},
		{"bool integer", oneInput(TypeInt32, 1), input("INT32", "[1]", "[true]"), "must be an integer"},
		{"int8 overflow", oneInput(TypeInt8, 1), input("INT8", "[1]", "[128]"), "does not fit INT8"},
		{"negative unsigned", oneInput(TypeUint16, 1), input("UINT16", "[1]", "[-1]"), "does not fit UINT16"},
		{"fp32 overflow", oneInput(TypeFP32, 1), input("FP32", "[1]", "[1e39]"), "does not fit FP32"},
		{"fp32 string", oneInput(TypeFP32, 1), input("FP32", "[1]", `["1"]`), "must be a number"},
		{"fp16 number", oneInput(TypeFP16, 1), input("FP16", "[1]", "[1.0]"), "FP16 input \"x\" must be sent as binary"},
		{"bf16 number", oneInput(TypeBF16, 1), input("BF16", "[1]", "[1.0]"), "BF16 input \"x\" must be sent as binary"},
		{"bytes number", oneInput(TypeBytes, 1), input("BYTES", "[1]", "[1]"), "BYTES must be a string"},
		{"unknown datatype", oneInput(TypeFP32, 1), input("FP8", "[1]", "[1]"), `unknown datatype "FP8"`},
		{"datatype mismatch", oneInput(TypeFP32, 1), input("FP64", "[1]", "[1]"), "model expects FP32"},
		{"shape rank mismatch", addSignature(), `{"inputs":[{"name":"A","shape":[6],"datatype":"FP32","data":[1,2,3,4,5,6]},{"name":"B","shape":[2,3],"datatype":"FP32","data":[1,2,3,4,5,6]}]}`, "rank 1 and the model expects 2"},
		{"data deeper than shape", oneInput(TypeFP32, 2, 1), input("FP32", "[2,1]", "[[[1]],[[2]]]"), "nesting does not match the shape rank"},
		{"data shallower than nested", oneInput(TypeFP32, 2, 1, 1), input("FP32", "[2,1,1]", "[[1],[2]]"), "nesting does not match the shape rank"},
		{"mixed nesting", oneInput(TypeFP32, 2, 1), input("FP32", "[2,1]", "[[1], 2]"), "nesting does not match"},
		{"mixed flat then nested", oneInput(TypeFP32, 2, 1), input("FP32", "[2,1]", "[1, [2]]"), "nesting does not match"},
		{"flat length not product", oneInput(TypeFP32, 2, 3), input("FP32", "[2,3]", "[1,2,3,4,5]"), "has 5 values and the shape needs 6"},
		{"flat too long", oneInput(TypeFP32, 2, 3), input("FP32", "[2,3]", "[1,2,3,4,5,6,7]"), "more values than the shape needs"},
		{"nested row too short", oneInput(TypeFP32, 2, 3), input("FP32", "[2,3]", "[[1,2],[3,4,5]]"), "dimension 1 has 2 values"},
		{"nested too few rows", oneInput(TypeFP32, 2, 3), input("FP32", "[2,3]", "[[1,2,3]]"), "dimension 0 has 1 values"},
		{"object in data", oneInput(TypeFP32, 1), input("FP32", "[1]", `[{"a":1}]`), "only arrays and values"},
		{"data not array", oneInput(TypeFP32, 1), input("FP32", "[1]", "1"), "data must be a JSON array"},
		{"variable dimension in request", oneInput(TypeFP32, -1), input("FP32", "[-1]", "[1]"), "negative dimension"},
		{"fixed dimension mismatch", oneInput(TypeFP32, 3), input("FP32", "[2]", "[1,2]"), "dimension 0 is 2 and the model expects 3"},
		{"fractional shape", oneInput(TypeFP32, 1), input("FP32", "[1.0]", "[1]"), "shape must hold integers"},
		{"shape overflow", oneInput(TypeFP32, -1, -1), input("FP32", "[4294967296,4294967296]", "[1]"), "too large"},
		{"unknown input", oneInput(TypeFP32, 1), `{"inputs":[{"name":"z","shape":[1],"datatype":"FP32","data":[1]}]}`, `unknown input "z"`},
		{"duplicate input", oneInput(TypeFP32, 1), `{"inputs":[{"name":"x","shape":[1],"datatype":"FP32","data":[1]},{"name":"x","shape":[1],"datatype":"FP32","data":[1]}]}`, `duplicate input "x"`},
		{"missing input", addSignature(), `{"inputs":[{"name":"A","shape":[2,3],"datatype":"FP32","data":[1,2,3,4,5,6]}]}`, `missing input "B"`},
		{"no data", oneInput(TypeFP32, 1), `{"inputs":[{"name":"x","shape":[1],"datatype":"FP32"}]}`, "neither data nor binary_data_size"},
		{"inputs not array", oneInput(TypeFP32, 1), `{"inputs":{}}`, "inputs must be a JSON array"},
		{"body not object", oneInput(TypeFP32, 1), `[1]`, "must be a JSON object"},
		{"body not JSON", oneInput(TypeFP32, 1), `{"inputs":`, "must be a JSON object"},
		{"id not string", oneInput(TypeFP32, 1), `{"id":1,"inputs":[{"name":"x","shape":[1],"datatype":"FP32","data":[1]}]}`, "id must be a string"},
		{"unknown output", oneInput(TypeFP32, 1), `{"inputs":[{"name":"x","shape":[1],"datatype":"FP32","data":[1]}],"outputs":[{"name":"q"}]}`, `unknown output "q"`},
		{"duplicate output", oneInput(TypeFP32, 1), `{"inputs":[{"name":"x","shape":[1],"datatype":"FP32","data":[1]}],"outputs":[{"name":"y"},{"name":"y"}]}`, `duplicate output "y"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := DecodeHTTP(c.sig, []byte(c.body), NoHeaderLength)
			wantInvalid(t, err, c.fragment)
		})
	}
}

func TestDecodeErrorsDoNotCarryTensorValues(t *testing.T) {
	body := `{"inputs":[{"name":"x","shape":[2],"datatype":"INT32","data":[1, 424242.5]}]}`
	_, err := DecodeHTTP(oneInput(TypeInt32, 2), []byte(body), NoHeaderLength)
	wantInvalid(t, err, "must be an integer")
	if bytes.Contains([]byte(err.Error()), []byte("424242")) {
		t.Fatalf("error carries a tensor value: %v", err)
	}
}

func TestDecodeBinarySuffix(t *testing.T) {
	a := fp32Bytes(1, 1, 1, 1, 1, 1)
	b := fp32Bytes(2, 2, 2, 2, 2, 2)
	header := `{"inputs":[
		{"name":"A","shape":[2,3],"datatype":"FP32","parameters":{"binary_data_size":24}},
		{"name":"B","shape":[2,3],"datatype":"FP32","parameters":{"binary_data_size":24}}],
		"outputs":[{"name":"C","parameters":{"binary_data":true}}]}`
	body, n := framed(header, a, b)
	req, err := DecodeHTTP(addSignature(), body, n)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(req.Inputs[0].Data, a) || !bytes.Equal(req.Inputs[1].Data, b) {
		t.Fatalf("inputs %+v", req.Inputs)
	}
	if cap(req.Inputs[0].Data) != len(a) {
		t.Fatalf("input A can grow into input B: cap %d", cap(req.Inputs[0].Data))
	}
	if !req.Outputs[0].Binary {
		t.Fatal("output C is not binary")
	}
}

func TestDecodeBinaryMixedWithJSON(t *testing.T) {
	header := `{"inputs":[
		{"name":"A","shape":[2,3],"datatype":"FP32","data":[1,1,1,1,1,1]},
		{"name":"B","shape":[2,3],"datatype":"FP32","parameters":{"binary_data_size":24}}]}`
	body, n := framed(header, fp32Bytes(2, 2, 2, 2, 2, 2))
	req, err := DecodeHTTP(addSignature(), body, n)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(req.Inputs[1].Data, fp32Bytes(2, 2, 2, 2, 2, 2)) {
		t.Fatalf("input B %v", req.Inputs[1].Data)
	}
}

func TestDecodeBinaryTypes(t *testing.T) {
	t.Run("BOOL", func(t *testing.T) {
		body, n := framed(`{"inputs":[{"name":"x","shape":[3],"datatype":"BOOL","parameters":{"binary_data_size":3}}]}`, []byte{1, 0, 1})
		req, err := DecodeHTTP(oneInput(TypeBool, 3), body, n)
		if err != nil || !bytes.Equal(req.Inputs[0].Data, []byte{1, 0, 1}) {
			t.Fatalf("decode: %v", err)
		}
	})
	t.Run("BYTES", func(t *testing.T) {
		data := bytesElements("ab", "", "c")
		body, n := framed(fmt.Sprintf(`{"inputs":[{"name":"x","shape":[3],"datatype":"BYTES","parameters":{"binary_data_size":%d}}]}`, len(data)), data)
		req, err := DecodeHTTP(oneInput(TypeBytes, 3), body, n)
		if err != nil || !bytes.Equal(req.Inputs[0].Data, data) {
			t.Fatalf("decode: %v", err)
		}
	})
	t.Run("FP16", func(t *testing.T) {
		body, n := framed(`{"inputs":[{"name":"x","shape":[2],"datatype":"FP16","parameters":{"binary_data_size":4}}]}`, []byte{0, 0x3c, 0, 0x40})
		if _, err := DecodeHTTP(oneInput(TypeFP16, 2), body, n); err != nil {
			t.Fatalf("decode: %v", err)
		}
	})
}

func TestDecodeBinaryRejects(t *testing.T) {
	fp32Header := func(sizeA, sizeB int) string {
		return fmt.Sprintf(`{"inputs":[
			{"name":"A","shape":[2,3],"datatype":"FP32","parameters":{"binary_data_size":%d}},
			{"name":"B","shape":[2,3],"datatype":"FP32","parameters":{"binary_data_size":%d}}]}`, sizeA, sizeB)
	}
	six := fp32Bytes(1, 2, 3, 4, 5, 6)
	bytesHeader := func(size int) string {
		return fmt.Sprintf(`{"inputs":[{"name":"x","shape":[2],"datatype":"BYTES","parameters":{"binary_data_size":%d}}]}`, size)
	}
	overrun := binary.LittleEndian.AppendUint32(bytesElements("ok"), 50)
	overrun = append(overrun, "short"...)
	cases := []struct {
		name     string
		sig      Signature
		header   string
		suffix   []byte
		fragment string
	}{
		{"short suffix", addSignature(), fp32Header(24, 24), append(slices.Clone(six), six[:20]...), `binary data for input "B" is shorter`},
		{"long suffix", addSignature(), fp32Header(24, 24), append(append(slices.Clone(six), six...), 0), "binary data is 49 bytes and the inputs declare 48"},
		{"size disagrees with shape", addSignature(), fp32Header(20, 24), append(six[:20:20], six...), `input "A" binary_data_size is 20 and its shape needs 24`},
		{"data and binary size", addSignature(), `{"inputs":[
			{"name":"A","shape":[2,3],"datatype":"FP32","data":[1,2,3,4,5,6],"parameters":{"binary_data_size":24}},
			{"name":"B","shape":[2,3],"datatype":"FP32","parameters":{"binary_data_size":24}}]}`, append(slices.Clone(six), six...), "both data and binary_data_size"},
		{"bool byte two", oneInput(TypeBool, 3), `{"inputs":[{"name":"x","shape":[3],"datatype":"BOOL","parameters":{"binary_data_size":3}}]}`, []byte{1, 2, 0}, "byte other than 0 or 1"},
		{"bytes length overruns", oneInput(TypeBytes, 2), bytesHeader(len(overrun)), overrun, "length overruns"},
		{"bytes length cut short", oneInput(TypeBytes, 2), bytesHeader(8), append(bytesElements("ok"), 1, 0), "cut short"},
		{"bytes count mismatch", oneInput(TypeBytes, 2), bytesHeader(6), bytesElements("ok"), "has 1 elements and the shape needs 2"},
		{"string binary size", oneInput(TypeBool, 1), `{"inputs":[{"name":"x","shape":[1],"datatype":"BOOL","parameters":{"binary_data_size":"1"}}]}`, []byte{1}, "non-negative integer"},
		{"negative binary size", oneInput(TypeBool, 1), `{"inputs":[{"name":"x","shape":[1],"datatype":"BOOL","parameters":{"binary_data_size":-1}}]}`, []byte{1}, "non-negative integer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, n := framed(c.header, c.suffix)
			_, err := DecodeHTTP(c.sig, body, n)
			wantInvalid(t, err, c.fragment)
		})
	}
}

func TestDecodeHeaderLength(t *testing.T) {
	header := `{"inputs":[{"name":"x","shape":[1],"datatype":"BOOL","parameters":{"binary_data_size":1}}]}`
	body, n := framed(header, []byte{1})
	sig := oneInput(TypeBool, 1)

	_, err := DecodeHTTP(sig, body, NoHeaderLength)
	wantInvalid(t, err, "must be a JSON object")
	_, err = DecodeHTTP(sig, []byte(header), NoHeaderLength)
	wantInvalid(t, err, "has no Inference-Header-Content-Length")
	_, err = DecodeHTTP(sig, body, int64(len(body))+1)
	wantInvalid(t, err, "outside the body")
	_, err = DecodeHTTP(sig, body, -2)
	wantInvalid(t, err, "outside the body")
	_, err = DecodeHTTP(sig, body, n-1)
	wantInvalid(t, err, "must be a JSON object")

	jsonOnly := `{"inputs":[{"name":"x","shape":[1],"datatype":"BOOL","data":[true]}]}`
	if _, err := DecodeHTTP(sig, []byte(jsonOnly), int64(len(jsonOnly))); err != nil {
		t.Fatalf("a header length covering the whole body was refused: %v", err)
	}
}

func TestDecodeDoesNotPanicOnTruncatedBodies(t *testing.T) {
	header := `{"id":"x","parameters":{"timeout":5},"inputs":[
		{"name":"A","shape":[2,3],"datatype":"FP32","data":[[1,2,3],[4,5,6]]},
		{"name":"B","shape":[2,3],"datatype":"FP32","parameters":{"binary_data_size":24}}],
		"outputs":[{"name":"C","parameters":{"binary_data":true}}]}`
	body, n := framed(header, fp32Bytes(1, 2, 3, 4, 5, 6))
	sig := addSignature()
	for i := range len(body) {
		for _, h := range []int64{NoHeaderLength, 0, n, int64(i)} {
			_, _ = DecodeHTTP(sig, body[:i], h)
		}
	}
}
