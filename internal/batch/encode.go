package batch

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
)

// ResponseOutput is one output tensor and how it is returned.
type ResponseOutput struct {
	Tensor
	// Binary reports whether the tensor bytes follow the JSON instead of appearing as JSON values.
	Binary bool
}

// Response is one infer result.
type Response struct {
	// ModelName is the model directory name.
	ModelName string
	// ModelVersion is the version that ran.
	ModelVersion string
	// ID is the request id, or empty.
	ID string
	// Outputs are in the order the request asked for them.
	Outputs []ResponseOutput
}

// Encoded is an HTTP infer response body.
type Encoded struct {
	// Body is the JSON document followed by the binary output data.
	Body []byte
	// HeaderLength is the byte length of the JSON document.
	HeaderLength int
	// Binary reports whether Body carries binary output data after the JSON.
	Binary bool
}

type responseJSON struct {
	ModelName    string       `json:"model_name"`
	ModelVersion string       `json:"model_version"`
	ID           string       `json:"id"`
	Outputs      []outputJSON `json:"outputs"`
}

type outputJSON struct {
	Name       string            `json:"name"`
	Datatype   Datatype          `json:"datatype"`
	Shape      []int64           `json:"shape"`
	Parameters *outputParamsJSON `json:"parameters,omitempty"`
	Data       any               `json:"data,omitempty"`
}

type outputParamsJSON struct {
	BinaryDataSize int `json:"binary_data_size"`
}

// EncodeHTTP writes an HTTP infer response.
// A binary output carries only binary_data_size in its parameters. No request parameter is echoed.
func EncodeHTTP(r Response) (Encoded, error) {
	doc := responseJSON{
		ModelName:    r.ModelName,
		ModelVersion: r.ModelVersion,
		ID:           r.ID,
		Outputs:      make([]outputJSON, len(r.Outputs)),
	}
	var suffix int
	for i, o := range r.Outputs {
		if err := CheckData(o.Datatype, o.Shape, o.Data); err != nil {
			return Encoded{}, fmt.Errorf("output %q: %w", o.Name, err)
		}
		shape := o.Shape
		if shape == nil {
			shape = []int64{}
		}
		oj := outputJSON{Name: o.Name, Datatype: o.Datatype, Shape: shape}
		if o.Binary {
			oj.Parameters = &outputParamsJSON{BinaryDataSize: len(o.Data)}
			suffix += len(o.Data)
		} else {
			if o.Datatype.BinaryOutputOnly() {
				return Encoded{}, invalidf("%s output %q is returned only as binary data", o.Datatype, o.Name)
			}
			oj.Data = jsonValues(o.Datatype, o.Data)
		}
		doc.Outputs[i] = oj
	}
	js, err := json.Marshal(doc)
	if err != nil {
		return Encoded{}, fmt.Errorf("encode response: %w", err)
	}
	enc := Encoded{HeaderLength: len(js), Binary: hasBinary(r.Outputs)}
	if !enc.Binary {
		enc.Body = js
		return enc, nil
	}
	enc.Body = make([]byte, 0, len(js)+suffix)
	enc.Body = append(enc.Body, js...)
	for _, o := range r.Outputs {
		if o.Binary {
			enc.Body = append(enc.Body, o.Data...)
		}
	}
	return enc, nil
}

func hasBinary(outputs []ResponseOutput) bool {
	for _, o := range outputs {
		if o.Binary {
			return true
		}
	}
	return false
}

// jsonValues returns the flat row-major JSON values of data, which CheckData already accepted.
func jsonValues(d Datatype, data []byte) any {
	le := binary.LittleEndian
	switch d {
	case TypeBool:
		out := make([]bool, len(data))
		for i, b := range data {
			out[i] = b == 1
		}
		return out
	case TypeBytes:
		out := []string{}
		for len(data) > 0 {
			n := le.Uint32(data)
			out = append(out, string(data[bytesLengthSize:bytesLengthSize+n]))
			data = data[bytesLengthSize+n:]
		}
		return out
	case TypeFP32:
		out := make([]float32, len(data)/4)
		for i := range out {
			out[i] = math.Float32frombits(le.Uint32(data[4*i:]))
		}
		return out
	case TypeFP64:
		out := make([]float64, len(data)/8)
		for i := range out {
			out[i] = math.Float64frombits(le.Uint64(data[8*i:]))
		}
		return out
	default:
	}
	size := d.ElementSize()
	out := make([]int64, len(data)/size)
	for i := range out {
		b := data[size*i:]
		switch d {
		case TypeInt8:
			out[i] = int64(int8(b[0])) // #nosec G115 -- stored two's-complement bits of an INT8
		case TypeInt16:
			out[i] = int64(int16(le.Uint16(b))) // #nosec G115 -- stored two's-complement bits of an INT16
		case TypeInt32:
			out[i] = int64(int32(le.Uint32(b))) // #nosec G115 -- stored two's-complement bits of an INT32
		case TypeUint8:
			out[i] = int64(b[0])
		case TypeUint16:
			out[i] = int64(le.Uint16(b))
		default:
			out[i] = int64(le.Uint32(b))
		}
	}
	return out
}
