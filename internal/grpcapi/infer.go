package grpcapi

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/api/openinference/v2"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
)

const payloadTooLarge = "request body is larger than the payload limit"

// Runner submits one decoded infer request to the queue of a model version.
// version is the version that serves the call.
type Runner interface {
	Run(ctx context.Context, name, version string, req *batch.Request) (*batch.Reply, error)
}

// InferConfig is the infer service.
type InferConfig struct {
	// Catalog is the model snapshot used to choose a version and check the tensors.
	// A nil catalog keeps the catalog already registered.
	Catalog Catalog
	// Runner queues the decoded request. A nil runner reports that the model was not found.
	Runner Runner
	// MaxPayloadBytes is the largest tensor this server accepts.
	// A larger tensor is rejected and is not queued.
	// The gRPC receive and send limits use the same value.
	// Call RegisterInfer before Serve so those limits are in place.
	MaxPayloadBytes int
	// Logger records infer outcomes. A nil logger records nothing.
	// Success is debug. A rejected caller is warn. A run that did not finish is error.
	// The line names the transport, model, version, request id, status, gRPC code, and duration.
	// It does not include the tensor body or the caller's credential.
	Logger *slog.Logger
}

// RegisterInfer serves infer from the model snapshot.
// A nil catalog keeps the catalog already registered.
func (s *Server) RegisterInfer(cfg InferConfig) {
	if s == nil {
		return
	}
	if cfg.Catalog != nil {
		s.catalog = cfg.Catalog
	}
	s.runner = cfg.Runner
	s.payload = cfg.MaxPayloadBytes
	s.logger = cfg.Logger
}

// ModelInfer runs one request on the selected model version.
// An empty model version is the version the policy selected.
func (s *Server) ModelInfer(ctx context.Context, req *openinference.ModelInferRequest) (*openinference.ModelInferResponse, error) {
	started := time.Now()
	name := req.GetModelName()
	requested := req.GetModelVersion()
	id := req.GetId()
	dir, chosen, err := batch.Select(s.directories(), name, requested)
	logged := requested
	if err == nil {
		logged = chosen.Version
	}
	if err != nil {
		batch.NoteFailure(s.runner, dir.Name, chosen.Version, err)
		s.logInfer(ctx, name, logged, id, started, err)
		return nil, statusFrom(err)
	}
	decoded, err := decodeInfer(chosen.Signature, req, s.payload)
	if err != nil {
		batch.NoteFailure(s.runner, dir.Name, chosen.Version, err)
		s.logInfer(ctx, name, logged, id, started, err)
		return nil, statusFrom(err)
	}
	reply, err := s.run(ctx, dir.Name, chosen.Version, decoded)
	if reply != nil {
		defer reply.Release()
	}
	if err != nil {
		s.logInfer(ctx, name, logged, id, started, err)
		return nil, statusFrom(err)
	}
	outputs, err := responseOutputs(decoded, reply)
	if err != nil {
		s.logInfer(ctx, name, logged, id, started, err)
		return nil, statusFrom(err)
	}
	encoded, err := encodeResponse(dir.Name, chosen.Version, decoded.ID, outputs)
	if err != nil {
		s.logInfer(ctx, name, logged, id, started, err)
		return nil, statusFrom(err)
	}
	s.logInfer(ctx, name, logged, id, started, nil)
	return encoded, nil
}

func (s *Server) run(ctx context.Context, name, version string, req *batch.Request) (*batch.Reply, error) {
	if s == nil || s.runner == nil {
		return nil, batch.NotFound(fmt.Sprintf("model %q was not found", name))
	}
	return s.runner.Run(ctx, name, version, req)
}

func (s *Server) logInfer(ctx context.Context, name, version, id string, started time.Time, err error) {
	if s == nil {
		return
	}
	batch.LogInfer(ctx, s.logger, "grpc", name, version, id, started, err)
}

func decodeInfer(sig batch.Signature, req *openinference.ModelInferRequest, payload int) (*batch.Request, error) {
	if req == nil {
		return nil, &batch.InvalidError{Reason: "infer request is missing"}
	}
	params, err := batch.ParseRequestParameters(paramsFrom(req.GetParameters()))
	if err != nil {
		return nil, err
	}
	inputs, err := decodeInputs(sig, req, payload)
	if err != nil {
		return nil, err
	}
	outputs, err := decodeOutputs(sig, req, params)
	if err != nil {
		return nil, err
	}
	return &batch.Request{
		ID:         req.GetId(),
		Parameters: params,
		Inputs:     inputs,
		Outputs:    outputs,
	}, nil
}

func decodeInputs(sig batch.Signature, req *openinference.ModelInferRequest, payload int) ([]batch.Tensor, error) {
	items := req.GetInputs()
	raw := req.GetRawInputContents()
	useRaw := len(raw) > 0
	if useRaw {
		for _, item := range items {
			if item.GetContents() != nil {
				return nil, &batch.InvalidError{Reason: "request sets raw input contents and tensor contents"}
			}
		}
		if len(raw) != len(items) {
			return nil, &batch.InvalidError{Reason: fmt.Sprintf("raw input contents has %d tensors and the request has %d inputs", len(raw), len(items))}
		}
	}
	index := make(map[string]int, len(sig.Inputs))
	for i, spec := range sig.Inputs {
		index[spec.Name] = i
	}
	out := make([]batch.Tensor, len(sig.Inputs))
	seen := make([]bool, len(sig.Inputs))
	var total int64
	limit := payloadLimit(payload)
	for i, item := range items {
		if item == nil {
			return nil, &batch.InvalidError{Reason: "infer input is missing"}
		}
		name := item.GetName()
		pos, ok := index[name]
		if !ok {
			return nil, &batch.InvalidError{Reason: fmt.Sprintf("unknown input %q", name)}
		}
		if seen[pos] {
			return nil, &batch.InvalidError{Reason: fmt.Sprintf("duplicate input %q", name)}
		}
		seen[pos] = true
		spec := sig.Inputs[pos]
		d, err := batch.ParseDatatype(item.GetDatatype())
		if err != nil {
			return nil, err
		}
		if d != spec.Datatype {
			return nil, &batch.InvalidError{Reason: fmt.Sprintf("input %q datatype is %s and the model expects %s", spec.Name, d, spec.Datatype)}
		}
		shape := append([]int64(nil), item.GetShape()...)
		if err = shapeMatches(spec, shape, sig.MaxBatchSize); err != nil {
			return nil, err
		}
		if _, err = batch.ParseInputParameters(name, paramsFrom(item.GetParameters())); err != nil {
			return nil, err
		}
		size, err := declaredSize(d, shape)
		if err != nil {
			return nil, err
		}
		if size >= 0 && size > limit {
			return nil, payloadError()
		}
		var data []byte
		if useRaw {
			data = raw[i]
		} else {
			data, err = contentsData(spec.Name, d, shape, item.GetContents(), limit)
			if err != nil {
				return nil, err
			}
		}
		if int64(len(data)) > limit || total > limit-int64(len(data)) {
			return nil, payloadError()
		}
		total += int64(len(data))
		if err = batch.CheckData(d, shape, data); err != nil {
			return nil, err
		}
		out[pos] = batch.Tensor{Name: spec.Name, Datatype: d, Shape: shape, Data: data}
	}
	for i, ok := range seen {
		if !ok {
			return nil, &batch.InvalidError{Reason: fmt.Sprintf("missing input %q", sig.Inputs[i].Name)}
		}
	}
	return out, nil
}

func shapeMatches(spec batch.TensorSpec, shape []int64, maxBatchSize int) error {
	if len(shape) != len(spec.Shape) {
		return &batch.InvalidError{Reason: fmt.Sprintf("input %q has rank %d and the model expects %d", spec.Name, len(shape), len(spec.Shape))}
	}
	for i, dim := range shape {
		if dim < 0 {
			return &batch.InvalidError{Reason: fmt.Sprintf("input %q has a negative dimension", spec.Name)}
		}
		if i == 0 && maxBatchSize > 0 {
			continue
		}
		if want := spec.Shape[i]; want >= 0 && dim != want {
			return &batch.InvalidError{Reason: fmt.Sprintf("input %q dimension %d is %d and the model expects %d", spec.Name, i, dim, want)}
		}
	}
	return nil
}

func declaredSize(d batch.Datatype, shape []int64) (int64, error) {
	if d == batch.TypeBytes {
		return -1, nil
	}
	count, err := batch.ElementCount(shape)
	if err != nil {
		return 0, err
	}
	size := int64(d.ElementSize())
	if size <= 0 {
		return 0, &batch.InvalidError{Reason: fmt.Sprintf("unknown datatype %q", d)}
	}
	if count > math.MaxInt64/size {
		return 0, payloadError()
	}
	return count * size, nil
}

func contentsData(name string, d batch.Datatype, shape []int64, c *openinference.InferTensorContents, limit int64) ([]byte, error) {
	if !d.JSONInput() {
		return nil, &batch.InvalidError{Reason: fmt.Sprintf("%s input %q must be sent as raw contents", d, name)}
	}
	count, err := batch.ElementCount(shape)
	if err != nil {
		return nil, err
	}
	kind := contentsKind(c)
	if kind == kindMany {
		return nil, &batch.InvalidError{Reason: fmt.Sprintf("input %q sets more than one contents field", name)}
	}
	if kind == kindNone {
		if count == 0 {
			return []byte{}, nil
		}
		return nil, &batch.InvalidError{Reason: fmt.Sprintf("input %q is missing tensor contents", name)}
	}
	if kind != kindFor(d) {
		return nil, &batch.InvalidError{Reason: fmt.Sprintf("input %q contents do not match %s", name, d)}
	}
	if d == batch.TypeBytes {
		n := bytesSize(c.GetBytesContents())
		if n > limit {
			return nil, payloadError()
		}
	}
	return packContents(d, c)
}

func decodeOutputs(sig batch.Signature, req *openinference.ModelInferRequest, rp batch.RequestParameters) ([]batch.RequestedOutput, error) {
	items := req.GetOutputs()
	if len(items) == 0 {
		out := make([]batch.RequestedOutput, len(sig.Outputs))
		for i, spec := range sig.Outputs {
			raw, err := outputRaw(spec, batch.OutputParameters{}, rp)
			if err != nil {
				return nil, err
			}
			out[i] = batch.RequestedOutput{Name: spec.Name, Datatype: spec.Datatype, Binary: raw}
		}
		return out, nil
	}
	index := make(map[string]int, len(sig.Outputs))
	for i, spec := range sig.Outputs {
		index[spec.Name] = i
	}
	seen := make([]bool, len(sig.Outputs))
	out := make([]batch.RequestedOutput, 0, len(items))
	for _, item := range items {
		if item == nil {
			return nil, &batch.InvalidError{Reason: "requested output is missing"}
		}
		name := item.GetName()
		pos, ok := index[name]
		if !ok {
			return nil, &batch.InvalidError{Reason: fmt.Sprintf("unknown output %q", name)}
		}
		if seen[pos] {
			return nil, &batch.InvalidError{Reason: fmt.Sprintf("duplicate output %q", name)}
		}
		seen[pos] = true
		op, err := batch.ParseOutputParameters(name, paramsFrom(item.GetParameters()))
		if err != nil {
			return nil, err
		}
		spec := sig.Outputs[pos]
		raw, err := outputRaw(spec, op, rp)
		if err != nil {
			return nil, err
		}
		out = append(out, batch.RequestedOutput{Name: spec.Name, Datatype: spec.Datatype, Binary: raw})
	}
	return out, nil
}

func outputRaw(spec batch.TensorSpec, op batch.OutputParameters, rp batch.RequestParameters) (bool, error) {
	asRaw, set := op.Binary, op.BinarySet
	if !set {
		asRaw, set = rp.BinaryOutput, rp.BinaryOutputSet
	}
	if !set {
		return spec.Datatype.BinaryOutputOnly(), nil
	}
	if !asRaw && spec.Datatype.BinaryOutputOnly() {
		return false, &batch.InvalidError{Reason: fmt.Sprintf("%s output %q is returned only as binary data", spec.Datatype, spec.Name)}
	}
	return asRaw, nil
}

func responseOutputs(req *batch.Request, reply *batch.Reply) ([]batch.ResponseOutput, error) {
	if reply == nil {
		return nil, errors.New("model returned no outputs")
	}
	byName := make(map[string]batch.Tensor, len(reply.Outputs))
	for _, out := range reply.Outputs {
		byName[out.Name] = out
	}
	outputs := make([]batch.ResponseOutput, len(req.Outputs))
	for i, want := range req.Outputs {
		got, ok := byName[want.Name]
		if !ok {
			return nil, fmt.Errorf("output %q was not produced", want.Name)
		}
		shape := got.Shape
		if shape == nil {
			shape = []int64{}
		}
		outputs[i] = batch.ResponseOutput{
			Tensor: batch.Tensor{
				Name:     want.Name,
				Datatype: want.Datatype,
				Shape:    shape,
				Data:     got.Data,
			},
			Binary: want.Binary,
		}
	}
	return outputs, nil
}

func encodeResponse(name, version, id string, outputs []batch.ResponseOutput) (*openinference.ModelInferResponse, error) {
	resp := &openinference.ModelInferResponse{
		ModelName:    name,
		ModelVersion: version,
		Id:           id,
		Outputs:      make([]*openinference.ModelInferResponse_InferOutputTensor, len(outputs)),
	}
	rawAll := false
	for _, out := range outputs {
		if out.Binary {
			rawAll = true
			break
		}
	}
	if rawAll {
		resp.RawOutputContents = make([][]byte, len(outputs))
	}
	for i, out := range outputs {
		if err := batch.CheckData(out.Datatype, out.Shape, out.Data); err != nil {
			return nil, fmt.Errorf("output %q: %w", out.Name, err)
		}
		shape := append([]int64(nil), out.Shape...)
		if shape == nil {
			shape = []int64{}
		}
		tensor := &openinference.ModelInferResponse_InferOutputTensor{
			Name:     out.Name,
			Datatype: string(out.Datatype),
			Shape:    shape,
		}
		copied := append([]byte(nil), out.Data...)
		if rawAll {
			resp.RawOutputContents[i] = copied
		} else {
			contents, err := contentsMessage(out.Datatype, copied)
			if err != nil {
				return nil, err
			}
			tensor.Contents = contents
		}
		resp.Outputs[i] = tensor
	}
	return resp, nil
}

func paramsFrom(in map[string]*openinference.InferParameter) map[string]batch.Param {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]batch.Param, len(in))
	for key, value := range in {
		out[key] = paramFrom(value)
	}
	return out
}

func paramFrom(p *openinference.InferParameter) batch.Param {
	switch choice := p.GetParameterChoice().(type) {
	case *openinference.InferParameter_BoolParam:
		return batch.BoolParam(choice.BoolParam)
	case *openinference.InferParameter_Int64Param:
		return batch.IntParam(choice.Int64Param)
	case *openinference.InferParameter_Uint64Param:
		return batch.UintParam(choice.Uint64Param)
	case *openinference.InferParameter_DoubleParam:
		return batch.FloatParam(choice.DoubleParam)
	case *openinference.InferParameter_StringParam:
		return batch.StringParam(choice.StringParam)
	default:
		return batch.Param{}
	}
}

func payloadLimit(n int) int64 {
	if n < 0 {
		return 0
	}
	return int64(n)
}

func payloadError() error {
	return &batch.InvalidError{Reason: payloadTooLarge}
}

type contentKind int

const (
	kindNone contentKind = iota
	kindBool
	kindInt
	kindInt64
	kindUint
	kindUint64
	kindFP32
	kindFP64
	kindBytes
	kindMany
)

func contentsKind(c *openinference.InferTensorContents) contentKind {
	if c == nil {
		return kindNone
	}
	var kind contentKind
	set := func(next contentKind, n int) {
		if n == 0 {
			return
		}
		if kind != kindNone {
			kind = kindMany
			return
		}
		kind = next
	}
	set(kindBool, len(c.GetBoolContents()))
	set(kindInt, len(c.GetIntContents()))
	set(kindInt64, len(c.GetInt64Contents()))
	set(kindUint, len(c.GetUintContents()))
	set(kindUint64, len(c.GetUint64Contents()))
	set(kindFP32, len(c.GetFp32Contents()))
	set(kindFP64, len(c.GetFp64Contents()))
	set(kindBytes, len(c.GetBytesContents()))
	return kind
}

func kindFor(d batch.Datatype) contentKind {
	switch d {
	case batch.TypeBool:
		return kindBool
	case batch.TypeInt8, batch.TypeInt16, batch.TypeInt32:
		return kindInt
	case batch.TypeInt64:
		return kindInt64
	case batch.TypeUint8, batch.TypeUint16, batch.TypeUint32:
		return kindUint
	case batch.TypeUint64:
		return kindUint64
	case batch.TypeFP32:
		return kindFP32
	case batch.TypeFP64:
		return kindFP64
	case batch.TypeBytes:
		return kindBytes
	default:
		return kindNone
	}
}

func bytesSize(values [][]byte) int64 {
	var n int64
	for _, value := range values {
		n += 4 + int64(len(value))
	}
	return n
}

func packContents(d batch.Datatype, c *openinference.InferTensorContents) ([]byte, error) {
	switch d {
	case batch.TypeBool:
		out := make([]byte, len(c.GetBoolContents()))
		for i, value := range c.GetBoolContents() {
			if value {
				out[i] = 1
			}
		}
		return out, nil
	case batch.TypeInt8, batch.TypeInt16, batch.TypeInt32:
		return packSigned(d, c.GetIntContents())
	case batch.TypeInt64:
		out := make([]byte, 8*len(c.GetInt64Contents()))
		for i, value := range c.GetInt64Contents() {
			binary.LittleEndian.PutUint64(out[i*8:], uint64(value)) // #nosec G115 -- two's-complement bits of an INT64
		}
		return out, nil
	case batch.TypeUint8, batch.TypeUint16, batch.TypeUint32:
		return packUnsigned(d, c.GetUintContents())
	case batch.TypeUint64:
		out := make([]byte, 8*len(c.GetUint64Contents()))
		for i, value := range c.GetUint64Contents() {
			binary.LittleEndian.PutUint64(out[i*8:], value)
		}
		return out, nil
	case batch.TypeFP32:
		out := make([]byte, 4*len(c.GetFp32Contents()))
		for i, value := range c.GetFp32Contents() {
			binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(value))
		}
		return out, nil
	case batch.TypeFP64:
		out := make([]byte, 8*len(c.GetFp64Contents()))
		for i, value := range c.GetFp64Contents() {
			binary.LittleEndian.PutUint64(out[i*8:], math.Float64bits(value))
		}
		return out, nil
	case batch.TypeBytes:
		values := c.GetBytesContents()
		out := make([]byte, 0, bytesSize(values))
		for _, value := range values {
			if uint64(len(value)) > math.MaxUint32 {
				return nil, &batch.InvalidError{Reason: "BYTES element is too long"}
			}
			var prefix [4]byte
			binary.LittleEndian.PutUint32(prefix[:], uint32(len(value))) // #nosec G115 -- length is checked against MaxUint32 above
			out = append(out, prefix[:]...)
			out = append(out, value...)
		}
		return out, nil
	default:
		return nil, &batch.InvalidError{Reason: fmt.Sprintf("%s input must be sent as raw contents", d)}
	}
}

func packSigned(d batch.Datatype, values []int32) ([]byte, error) {
	size := d.ElementSize()
	out := make([]byte, len(values)*size)
	for i, value := range values {
		switch d {
		case batch.TypeInt8:
			if value < math.MinInt8 || value > math.MaxInt8 {
				return nil, &batch.InvalidError{Reason: "integer does not fit INT8"}
			}
			out[i] = byte(int8(value)) // #nosec G115 -- range checked above; two's-complement bits of an INT8
		case batch.TypeInt16:
			if value < math.MinInt16 || value > math.MaxInt16 {
				return nil, &batch.InvalidError{Reason: "integer does not fit INT16"}
			}
			binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(value))) // #nosec G115 -- range checked above; two's-complement bits of an INT16
		default:
			binary.LittleEndian.PutUint32(out[i*4:], uint32(value)) // #nosec G115 -- two's-complement bits of an INT32
		}
	}
	return out, nil
}

func packUnsigned(d batch.Datatype, values []uint32) ([]byte, error) {
	size := d.ElementSize()
	out := make([]byte, len(values)*size)
	for i, value := range values {
		switch d {
		case batch.TypeUint8:
			if value > math.MaxUint8 {
				return nil, &batch.InvalidError{Reason: "integer does not fit UINT8"}
			}
			out[i] = byte(value)
		case batch.TypeUint16:
			if value > math.MaxUint16 {
				return nil, &batch.InvalidError{Reason: "integer does not fit UINT16"}
			}
			binary.LittleEndian.PutUint16(out[i*2:], uint16(value))
		default:
			binary.LittleEndian.PutUint32(out[i*4:], value)
		}
	}
	return out, nil
}

func contentsMessage(d batch.Datatype, data []byte) (*openinference.InferTensorContents, error) {
	le := binary.LittleEndian
	switch d {
	case batch.TypeBool:
		out := make([]bool, len(data))
		for i, value := range data {
			out[i] = value == 1
		}
		return &openinference.InferTensorContents{BoolContents: out}, nil
	case batch.TypeInt8:
		out := make([]int32, len(data))
		for i, value := range data {
			out[i] = int32(int8(value)) // #nosec G115 -- stored two's-complement bits of an INT8
		}
		return &openinference.InferTensorContents{IntContents: out}, nil
	case batch.TypeInt16:
		out := make([]int32, len(data)/2)
		for i := range out {
			out[i] = int32(int16(le.Uint16(data[i*2:]))) // #nosec G115 -- stored two's-complement bits of an INT16
		}
		return &openinference.InferTensorContents{IntContents: out}, nil
	case batch.TypeInt32:
		out := make([]int32, len(data)/4)
		for i := range out {
			out[i] = int32(le.Uint32(data[i*4:])) // #nosec G115 -- stored two's-complement bits of an INT32
		}
		return &openinference.InferTensorContents{IntContents: out}, nil
	case batch.TypeInt64:
		out := make([]int64, len(data)/8)
		for i := range out {
			out[i] = int64(le.Uint64(data[i*8:])) // #nosec G115 -- stored two's-complement bits of an INT64
		}
		return &openinference.InferTensorContents{Int64Contents: out}, nil
	case batch.TypeUint8:
		out := make([]uint32, len(data))
		for i, value := range data {
			out[i] = uint32(value)
		}
		return &openinference.InferTensorContents{UintContents: out}, nil
	case batch.TypeUint16:
		out := make([]uint32, len(data)/2)
		for i := range out {
			out[i] = uint32(le.Uint16(data[i*2:]))
		}
		return &openinference.InferTensorContents{UintContents: out}, nil
	case batch.TypeUint32:
		out := make([]uint32, len(data)/4)
		for i := range out {
			out[i] = le.Uint32(data[i*4:])
		}
		return &openinference.InferTensorContents{UintContents: out}, nil
	case batch.TypeUint64:
		out := make([]uint64, len(data)/8)
		for i := range out {
			out[i] = le.Uint64(data[i*8:])
		}
		return &openinference.InferTensorContents{Uint64Contents: out}, nil
	case batch.TypeFP32:
		out := make([]float32, len(data)/4)
		for i := range out {
			out[i] = math.Float32frombits(le.Uint32(data[i*4:]))
		}
		return &openinference.InferTensorContents{Fp32Contents: out}, nil
	case batch.TypeFP64:
		out := make([]float64, len(data)/8)
		for i := range out {
			out[i] = math.Float64frombits(le.Uint64(data[i*8:]))
		}
		return &openinference.InferTensorContents{Fp64Contents: out}, nil
	case batch.TypeBytes:
		var out [][]byte
		for len(data) > 0 {
			n := le.Uint32(data)
			out = append(out, append([]byte(nil), data[4:4+n]...))
			data = data[4+n:]
		}
		return &openinference.InferTensorContents{BytesContents: out}, nil
	default:
		return nil, &batch.InvalidError{Reason: fmt.Sprintf("%s output is returned only as raw contents", d)}
	}
}
