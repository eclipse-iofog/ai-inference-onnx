package batch

import (
	"bytes"
	"fmt"
	"math"
	"testing"
)

const oneFP32 = `{"name":"x","shape":[1],"datatype":"FP32","data":[1]}`

func TestRequestParametersRoundTripAsInt64(t *testing.T) {
	cases := []struct {
		params   string
		timeout  int64
		priority int64
	}{
		{`{}`, 0, 0},
		{`{"timeout":0,"priority":0}`, 0, 0},
		{`{"timeout":1500,"priority":1}`, 1500, 1},
		{`{"timeout":9223372036854775807,"priority":-9223372036854775808}`, math.MaxInt64, math.MinInt64},
		{`{"priority":-3}`, 0, -3},
	}
	for _, c := range cases {
		req := mustDecode(t, oneInput(TypeFP32, 1), fmt.Sprintf(`{"parameters":%s,"inputs":[%s]}`, c.params, oneFP32))
		if req.Parameters.Timeout != c.timeout || req.Parameters.Priority != c.priority {
			t.Fatalf("%s: %+v", c.params, req.Parameters)
		}
	}
}

func TestParametersFromTypedValues(t *testing.T) {
	p, err := ParseRequestParameters(map[string]Param{
		"timeout":  UintParam(42),
		"priority": IntParam(-1),
		"headers":  StringParam("x"),
		"custom":   FloatParam(1.5),
	})
	if err != nil || p.Timeout != 42 || p.Priority != -1 {
		t.Fatalf("parameters %+v, %v", p, err)
	}
	_, err = ParseRequestParameters(map[string]Param{"timeout": UintParam(math.MaxUint64)})
	wantInvalid(t, err, "must be an integer")
	_, err = ParseRequestParameters(map[string]Param{"binary_data_output": IntParam(1)})
	wantInvalid(t, err, "must be a bool")
	_, err = ParseInputParameters("x", map[string]Param{"sequence_end": BoolParam(true)})
	wantInvalid(t, err, `input "x" parameter "sequence_end"`)
}

func TestParametersReject(t *testing.T) {
	request := func(params string) string {
		return fmt.Sprintf(`{"parameters":%s,"inputs":[%s]}`, params, oneFP32)
	}
	withInput := func(params string) string {
		return fmt.Sprintf(`{"inputs":[{"name":"x","shape":[1],"datatype":"FP32","data":[1],"parameters":%s}]}`, params)
	}
	withOutput := func(params string) string {
		return fmt.Sprintf(`{"inputs":[%s],"outputs":[{"name":"y","parameters":%s}]}`, oneFP32, params)
	}
	cases := []struct {
		name     string
		body     string
		fragment string
	}{
		{"request sequence_id", request(`{"sequence_id":1}`), `request parameter "sequence_id" is not supported`},
		{"request sequence_start", request(`{"sequence_start":true}`), `request parameter "sequence_start" is not supported`},
		{"request sequence_end", request(`{"sequence_end":false}`), `request parameter "sequence_end" is not supported`},
		{"input sequence_id", withInput(`{"sequence_id":"a"}`), `input "x" parameter "sequence_id" is not supported`},
		{"input sequence_start", withInput(`{"sequence_start":true}`), `input "x" parameter "sequence_start" is not supported`},
		{"output sequence_end", withOutput(`{"sequence_end":true}`), `output "y" parameter "sequence_end" is not supported`},
		{"string timeout", request(`{"timeout":"10"}`), `"timeout" must be an integer`},
		{"negative timeout", request(`{"timeout":-1}`), `"timeout" must not be negative`},
		{"fractional timeout", request(`{"timeout":1.5}`), `"timeout" must be an integer`},
		{"bool priority", request(`{"priority":true}`), `"priority" must be an integer`},
		{"string priority", request(`{"priority":"1"}`), `"priority" must be an integer`},
		{"string binary_data_output", request(`{"binary_data_output":"true"}`), `"binary_data_output" must be a bool`},
		{"numeric binary_data", withOutput(`{"binary_data":1}`), `output "y" parameter "binary_data" must be a bool`},
		{"string binary_data", withOutput(`{"binary_data":"true"}`), `parameter "binary_data" must be a bool`},
		{"parameters not object", request(`[1]`), "request parameters must be a JSON object"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := DecodeHTTP(oneInput(TypeFP32, 1), []byte(c.body), NoHeaderLength)
			wantInvalid(t, err, c.fragment)
		})
	}
}

func TestIgnoredParametersAreDroppedAndNotEchoed(t *testing.T) {
	body := `{"id":"r","parameters":{"headers":{"authorization":"Bearer leaked"},"triton_enable_empty_final_response":true,"custom":"v","timeout":3},
		"inputs":[{"name":"x","shape":[1],"datatype":"FP32","data":[1],"parameters":{"triton_x":1,"headers":"h"}}],
		"outputs":[{"name":"y","parameters":{"triton_y":"z","classification":2}}]}`
	req := mustDecode(t, oneInput(TypeFP32, 1), body)
	if req.Parameters != (RequestParameters{Timeout: 3}) {
		t.Fatalf("parameters %+v", req.Parameters)
	}
	enc, err := EncodeHTTP(Response{
		ModelName:    "m",
		ModelVersion: "1",
		ID:           req.ID,
		Outputs:      []ResponseOutput{{Tensor: Tensor{Name: "y", Datatype: TypeFP32, Shape: []int64{1}, Data: fp32Bytes(2)}, Binary: req.Outputs[0].Binary}},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for _, s := range []string{"headers", "triton_", "leaked", "custom", "classification", "timeout"} {
		if bytes.Contains(enc.Body, []byte(s)) {
			t.Fatalf("response carries %q: %s", s, enc.Body)
		}
	}
}

func TestOutputBinaryFlags(t *testing.T) {
	sig := Signature{
		Inputs: []TensorSpec{{Name: "x", Datatype: TypeFP32, Shape: []int64{1}}},
		Outputs: []TensorSpec{
			{Name: "f", Datatype: TypeFP32, Shape: []int64{1}},
			{Name: "g", Datatype: TypeFP32, Shape: []int64{1}},
		},
	}
	binaries := func(body string) []bool {
		t.Helper()
		req := mustDecode(t, sig, body)
		out := make([]bool, len(req.Outputs))
		for i, o := range req.Outputs {
			out[i] = o.Binary
		}
		return out
	}
	cases := []struct {
		name string
		body string
		want []bool
	}{
		{"default", fmt.Sprintf(`{"inputs":[%s]}`, oneFP32), []bool{false, false}},
		{"request flag", fmt.Sprintf(`{"parameters":{"binary_data_output":true},"inputs":[%s]}`, oneFP32), []bool{true, true}},
		{"output overrides true request flag", fmt.Sprintf(`{"parameters":{"binary_data_output":true},"inputs":[%s],"outputs":[{"name":"g","parameters":{"binary_data":false}},{"name":"f"}]}`, oneFP32), []bool{false, true}},
		{"output overrides false request flag", fmt.Sprintf(`{"parameters":{"binary_data_output":false},"inputs":[%s],"outputs":[{"name":"f","parameters":{"binary_data":true}},{"name":"g"}]}`, oneFP32), []bool{true, false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := binaries(c.body)
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Fatalf("binary %v, want %v", got, c.want)
			}
		})
	}
	req := mustDecode(t, sig, fmt.Sprintf(`{"inputs":[%s],"outputs":[{"name":"g"},{"name":"f"}]}`, oneFP32))
	if req.Outputs[0].Name != "g" || req.Outputs[1].Name != "f" {
		t.Fatalf("named outputs are not in request order: %+v", req.Outputs)
	}
}
