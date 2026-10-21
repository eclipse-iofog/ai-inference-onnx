package batch

import (
	"encoding/binary"
	"math"
)

// bytesLengthSize is the size of the little-endian length before each BYTES element.
const bytesLengthSize = 4

// Tensor is one decoded tensor.
type Tensor struct {
	// Name is the tensor name in the model.
	Name string
	// Datatype is the wire datatype.
	Datatype Datatype
	// Shape is the concrete shape.
	Shape []int64
	// Data is row-major and little-endian with no stride.
	// BOOL is one byte, 0 or 1. A BYTES element is a 4-byte little-endian length followed by its bytes.
	// Data may share memory with the body it was decoded from.
	Data []byte
}

// TensorSpec is one model input or output as the model declares it.
type TensorSpec struct {
	// Name is the tensor name in the model.
	Name string
	// Datatype is the wire datatype.
	Datatype Datatype
	// Shape uses -1 for a variable dimension.
	Shape []int64
}

// Signature is the inputs, outputs, and batch cap of one model version.
type Signature struct {
	// Inputs are in model order.
	Inputs []TensorSpec
	// Outputs are in model order.
	Outputs []TensorSpec
	// MaxBatchSize is the recorded batch cap. When it is above zero, dimension 0 of every tensor is the batch.
	MaxBatchSize int
}

// ElementCount returns the product of shape. A negative dimension or an overflow is an error.
func ElementCount(shape []int64) (int64, error) {
	count := int64(1)
	for _, d := range shape {
		if d < 0 {
			return 0, invalidf("shape has a negative dimension")
		}
		if d != 0 && count > math.MaxInt64/d {
			return 0, invalidf("shape element count is too large")
		}
		count *= d
	}
	return count, nil
}

// fixedByteSize returns the byte size of count elements of a fixed-size datatype.
func fixedByteSize(d Datatype, count int64) (int64, error) {
	size := int64(d.ElementSize())
	if count > math.MaxInt64/size {
		return 0, invalidf("tensor byte size is too large")
	}
	return count * size, nil
}

// CheckData reports whether data is the binary form of a tensor with this datatype and shape.
func CheckData(d Datatype, shape []int64, data []byte) error {
	if !d.Known() {
		return invalidf("unknown datatype %q", d)
	}
	count, err := ElementCount(shape)
	if err != nil {
		return err
	}
	if d == TypeBytes {
		return checkBytesData(count, data)
	}
	want, err := fixedByteSize(d, count)
	if err != nil {
		return err
	}
	if int64(len(data)) != want {
		return invalidf("binary data is %d bytes and the shape needs %d", len(data), want)
	}
	if d == TypeBool {
		for _, b := range data {
			if b > 1 {
				return invalidf("BOOL binary data has a byte other than 0 or 1")
			}
		}
	}
	return nil
}

// checkBytesData reports whether data is exactly count length-prefixed BYTES elements.
func checkBytesData(count int64, data []byte) error {
	var seen int64
	for len(data) > 0 {
		if len(data) < bytesLengthSize {
			return invalidf("BYTES element length is cut short")
		}
		n := uint64(binary.LittleEndian.Uint32(data))
		data = data[bytesLengthSize:]
		if n > uint64(len(data)) {
			return invalidf("BYTES element length overruns the data")
		}
		data = data[n:]
		seen++
	}
	if seen != count {
		return invalidf("BYTES data has %d elements and the shape needs %d", seen, count)
	}
	return nil
}

// checkShape reports whether a client shape fits spec.
// When maxBatchSize is above zero, dimension 0 is the client's batch and is not compared.
func checkShape(spec TensorSpec, shape []int64, maxBatchSize int) error {
	if len(shape) != len(spec.Shape) {
		return invalidf("input %q has rank %d and the model expects %d", spec.Name, len(shape), len(spec.Shape))
	}
	for i, d := range shape {
		if d < 0 {
			return invalidf("input %q has a negative dimension", spec.Name)
		}
		if i == 0 && maxBatchSize > 0 {
			continue
		}
		if want := spec.Shape[i]; want >= 0 && d != want {
			return invalidf("input %q dimension %d is %d and the model expects %d", spec.Name, i, d, want)
		}
	}
	return nil
}
