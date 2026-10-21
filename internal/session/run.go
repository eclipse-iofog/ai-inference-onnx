//go:build cgo

package session

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"unsafe"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/ort"
)

// Run executes one selected version.
// It holds that version's execution slot until it returns, so a second call waits.
// A context that is already canceled returns the context error and does not call the runtime.
// Output bytes are a copy in a pooled buffer. Input and output tensors are closed before Run returns.
func (r *Repository) Run(ctx context.Context, name, version string, inputs []Input) ([]Output, error) {
	if r == nil {
		return nil, errors.New("model was not found")
	}
	admission := r.Admit(name, version)
	if !admission.Proceed() {
		return nil, &StatusError{Status: admission.Status, Reason: admission.Reason}
	}
	r.mu.Lock()
	item := r.models[name]
	chosen, ok := item.lookup(admission.Version)
	r.mu.Unlock()
	if !ok {
		return nil, &StatusError{Status: StatusMissing, Reason: fmt.Sprintf("version %q was not found", version)}
	}
	return chosen.execute(ctx, r.pool, inputs)
}

func (v *versionSession) execute(ctx context.Context, pool *Pool, inputs []Input) ([]Output, error) {
	v.slot.Lock()
	defer v.slot.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if v.session == nil {
		return nil, errors.New("model session is closed")
	}
	tensors, pooled, backing, err := bindInputs(pool, v.inputs, inputs)
	if err != nil {
		return nil, err
	}
	defer releaseBound(tensors, pooled, pool)
	if beforeRuntime != nil {
		beforeRuntime(backing)
	}
	outputs, err := v.session.Run(ctx, tensors)
	if err != nil {
		return nil, err
	}
	defer closeTensorMap(outputs)
	return copyOutputs(pool, outputs)
}

func (v *versionSession) warm() error {
	infos := make([]ort.TensorInfo, 0, len(v.inputs)+len(v.outputs))
	infos = append(infos, v.inputs...)
	infos = append(infos, v.outputs...)
	if !warmupAllowed(infos) {
		return nil
	}
	inputs := make(map[string]*ort.Tensor, len(v.inputs))
	opened := make([]*ort.Tensor, 0, len(v.inputs))
	defer func() {
		for _, tensor := range opened {
			_ = tensor.Close()
		}
	}()
	for _, info := range v.inputs {
		tensor, err := zeroTensor(info)
		if err != nil {
			return err
		}
		opened = append(opened, tensor)
		inputs[info.Name] = tensor
	}
	outputs, err := v.session.Run(context.Background(), inputs)
	if err != nil {
		return err
	}
	for _, tensor := range outputs {
		opened = append(opened, tensor)
	}
	v.warmed = true
	return nil
}

func warmupAllowed(infos []ort.TensorInfo) bool {
	for _, info := range infos {
		for i, dim := range info.Shape {
			if dim < 0 && i != 0 {
				return false
			}
		}
	}
	return true
}

func zeroTensor(info ort.TensorInfo) (*ort.Tensor, error) {
	shape := slices.Clone(info.Shape)
	if len(shape) > 0 && shape[0] < 0 {
		shape[0] = 1
	}
	size, ok := numericSize(info.ElementType)
	if !ok {
		return nil, fmt.Errorf("element type %d is not a numeric tensor", info.ElementType)
	}
	count, err := elementCount(shape)
	if err != nil {
		return nil, err
	}
	if size > 0 && count > math.MaxInt/size {
		return nil, errors.New("tensor shape overflows")
	}
	return newNumericTensor(info.ElementType, shape, make([]byte, count*size))
}

func bindInputs(pool *Pool, want []ort.TensorInfo, inputs []Input) (map[string]*ort.Tensor, []Buffer, []tensorMemory, error) {
	byName := make(map[string]Input, len(inputs))
	for _, in := range inputs {
		if _, ok := byName[in.Name]; ok {
			return nil, nil, nil, fmt.Errorf("input %q is repeated", in.Name)
		}
		byName[in.Name] = in
	}
	if len(byName) != len(want) {
		return nil, nil, nil, errors.New("inputs do not match the model")
	}
	tensors := make(map[string]*ort.Tensor, len(want))
	var pooled []Buffer
	backing := make([]tensorMemory, 0, len(want))
	for _, info := range want {
		in, ok := byName[info.Name]
		if !ok {
			releaseBound(tensors, pooled, pool)
			return nil, nil, nil, fmt.Errorf("input %q is missing", info.Name)
		}
		tensor, buf, err := inputTensor(pool, info, in)
		if err != nil {
			releaseBound(tensors, pooled, pool)
			return nil, nil, nil, err
		}
		tensors[info.Name] = tensor
		if buf.Pooled {
			pooled = append(pooled, buf)
		}
		backing = append(backing, tensorMemory{name: info.Name, data: buf.Data})
	}
	return tensors, pooled, backing, nil
}

func inputTensor(pool *Pool, info ort.TensorInfo, in Input) (*ort.Tensor, Buffer, error) {
	size, ok := numericSize(info.ElementType)
	if !ok {
		return nil, Buffer{}, fmt.Errorf("input %q element type %d is not numeric", info.Name, info.ElementType)
	}
	count, err := elementCount(in.Shape)
	if err != nil {
		return nil, Buffer{}, fmt.Errorf("input %q: %w", info.Name, err)
	}
	if size > 0 && count > math.MaxInt/size {
		return nil, Buffer{}, fmt.Errorf("input %q shape overflows", info.Name)
	}
	need := count * size
	if len(in.Data) != need {
		return nil, Buffer{}, fmt.Errorf("input %q has %d bytes, want %d", info.Name, len(in.Data), need)
	}
	var buf Buffer
	raw := in.Data
	if need > 0 {
		buf, err = bufferFor(pool, in.Data, size)
		if err != nil {
			return nil, Buffer{}, err
		}
		raw = buf.Data
	}
	tensor, err := newNumericTensor(info.ElementType, in.Shape, raw)
	if err != nil {
		if buf.Pooled {
			pool.Put(buf)
		}
		return nil, Buffer{}, fmt.Errorf("input %q: %w", info.Name, err)
	}
	return tensor, buf, nil
}

func bufferFor(pool *Pool, raw []byte, size int) (Buffer, error) {
	var existing []byte
	if aligned(raw, size) {
		existing = raw
	}
	buf, err := pool.Get(len(raw), existing)
	if err != nil {
		return Buffer{}, err
	}
	if buf.Pooled {
		copy(buf.Data, raw)
	}
	return buf, nil
}

func aligned(raw []byte, size int) bool {
	if len(raw) == 0 || size <= 1 {
		return true
	}
	return uintptr(unsafe.Pointer(&raw[0]))%uintptr(size) == 0 // #nosec G103 -- address of the caller's tensor bytes, used only to test alignment
}

func copyOutputs(pool *Pool, outputs map[string]*ort.Tensor) ([]Output, error) {
	names := make([]string, 0, len(outputs))
	for name := range outputs {
		names = append(names, name)
	}
	slices.Sort(names)
	result := make([]Output, 0, len(names))
	for _, name := range names {
		tensor := outputs[name]
		if tensor == nil {
			putOutputs(pool, result)
			return nil, fmt.Errorf("output %q has no tensor", name)
		}
		raw, err := tensor.Bytes()
		if err != nil {
			putOutputs(pool, result)
			return nil, err
		}
		shape := tensor.Shape()
		if len(raw) == 0 {
			result = append(result, Output{Name: name, Shape: shape})
			continue
		}
		// Pass no existing slice. The tensor view is invalid once the tensor is closed.
		buf, err := pool.Get(len(raw), nil)
		if err != nil {
			putOutputs(pool, result)
			return nil, err
		}
		copy(buf.Data, raw)
		result = append(result, Output{Name: name, Shape: shape, Buffer: buf})
	}
	return result, nil
}

func putOutputs(pool *Pool, outputs []Output) {
	for _, out := range outputs {
		pool.Put(out.Buffer)
	}
}

func releaseBound(tensors map[string]*ort.Tensor, pooled []Buffer, pool *Pool) {
	closeTensorMap(tensors)
	for _, buf := range pooled {
		pool.Put(buf)
	}
}

func closeTensorMap(tensors map[string]*ort.Tensor) {
	for _, tensor := range tensors {
		_ = tensor.Close()
	}
}

func numericSize(elem ort.ElementType) (int, bool) {
	switch elem {
	case ort.ElementBool, ort.ElementUint8, ort.ElementInt8:
		return 1, true
	case ort.ElementUint16, ort.ElementInt16:
		return 2, true
	case ort.ElementFloat32, ort.ElementInt32, ort.ElementUint32:
		return 4, true
	case ort.ElementFloat64, ort.ElementInt64, ort.ElementUint64:
		return 8, true
	default:
		return 0, false
	}
}

func elementCount(shape []int64) (int, error) {
	n := 1
	for _, dim := range shape {
		if dim < 0 {
			return 0, errors.New("tensor shape is not concrete")
		}
		if dim > 0 && n > math.MaxInt/int(dim) {
			return 0, errors.New("tensor shape overflows")
		}
		n *= int(dim)
	}
	return n, nil
}

func newNumericTensor(elem ort.ElementType, shape []int64, raw []byte) (*ort.Tensor, error) {
	switch elem {
	case ort.ElementFloat32:
		return ort.NewTensor[float32](shape, asSlice[float32](raw))
	case ort.ElementFloat64:
		return ort.NewTensor[float64](shape, asSlice[float64](raw))
	case ort.ElementInt8:
		return ort.NewTensor[int8](shape, asSlice[int8](raw))
	case ort.ElementUint8:
		return ort.NewTensor[uint8](shape, asSlice[uint8](raw))
	case ort.ElementInt16:
		return ort.NewTensor[int16](shape, asSlice[int16](raw))
	case ort.ElementUint16:
		return ort.NewTensor[uint16](shape, asSlice[uint16](raw))
	case ort.ElementInt32:
		return ort.NewTensor[int32](shape, asSlice[int32](raw))
	case ort.ElementUint32:
		return ort.NewTensor[uint32](shape, asSlice[uint32](raw))
	case ort.ElementInt64:
		return ort.NewTensor[int64](shape, asSlice[int64](raw))
	case ort.ElementUint64:
		return ort.NewTensor[uint64](shape, asSlice[uint64](raw))
	case ort.ElementBool:
		return ort.NewTensor[bool](shape, asSlice[bool](raw))
	default:
		return nil, fmt.Errorf("element type %d is not a numeric tensor", elem)
	}
}

func asSlice[T any](raw []byte) []T {
	if len(raw) == 0 {
		return nil
	}
	var zero T
	return unsafe.Slice((*T)(unsafe.Pointer(&raw[0])), len(raw)/int(unsafe.Sizeof(zero))) // #nosec G103 -- tensor bytes are the numeric elements, viewed without a copy
}
