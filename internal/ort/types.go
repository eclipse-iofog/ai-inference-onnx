package ort

// CPUExecutionProvider is the CPU execution provider.
const CPUExecutionProvider = "CPUExecutionProvider"

// ElementType is the ONNX element type of one tensor.
type ElementType int

const (
	ElementUndefined ElementType = 0
	ElementFloat32   ElementType = 1
	ElementUint8     ElementType = 2
	ElementInt8      ElementType = 3
	ElementUint16    ElementType = 4
	ElementInt16     ElementType = 5
	ElementInt32     ElementType = 6
	ElementInt64     ElementType = 7
	ElementString    ElementType = 8
	ElementBool      ElementType = 9
	ElementFloat16   ElementType = 10
	ElementFloat64   ElementType = 11
	ElementUint32    ElementType = 12
	ElementUint64    ElementType = 13
	ElementBFloat16  ElementType = 16
)

// TensorElement is a Go type that maps directly to an ONNX tensor element.
type TensorElement interface {
	bool | int8 | uint8 | int16 | uint16 | int32 | uint32 | int64 | uint64 | float32 | float64
}

// TensorInfo describes one session input or output.
type TensorInfo struct {
	// Name is the tensor name in the model.
	Name string
	// ElementType is the ONNX element type.
	ElementType ElementType
	// Shape is the tensor shape. A negative length is a dynamic dimension.
	Shape []int64
}
