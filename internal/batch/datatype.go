// Package batch decodes and encodes inference tensors, and lists model directories.
//
// It does not import a transport server, the session package, or ONNX Runtime.
package batch

// Datatype is a wire tensor datatype name.
type Datatype string

// The supported datatypes.
const (
	TypeBool   Datatype = "BOOL"
	TypeUint8  Datatype = "UINT8"
	TypeUint16 Datatype = "UINT16"
	TypeUint32 Datatype = "UINT32"
	TypeUint64 Datatype = "UINT64"
	TypeInt8   Datatype = "INT8"
	TypeInt16  Datatype = "INT16"
	TypeInt32  Datatype = "INT32"
	TypeInt64  Datatype = "INT64"
	TypeFP16   Datatype = "FP16"
	TypeFP32   Datatype = "FP32"
	TypeFP64   Datatype = "FP64"
	TypeBytes  Datatype = "BYTES"
	TypeBF16   Datatype = "BF16"
)

// elementSizes is the byte size of one element. BYTES has no fixed size.
var elementSizes = map[Datatype]int{
	TypeBool:   1,
	TypeUint8:  1,
	TypeUint16: 2,
	TypeUint32: 4,
	TypeUint64: 8,
	TypeInt8:   1,
	TypeInt16:  2,
	TypeInt32:  4,
	TypeInt64:  8,
	TypeFP16:   2,
	TypeFP32:   4,
	TypeFP64:   8,
	TypeBytes:  0,
	TypeBF16:   2,
}

// ParseDatatype returns the datatype named s.
func ParseDatatype(s string) (Datatype, error) {
	d := Datatype(s)
	if !d.Known() {
		return "", invalidf("unknown datatype %q", s)
	}
	return d, nil
}

// Known reports whether d is a supported datatype.
func (d Datatype) Known() bool {
	_, ok := elementSizes[d]
	return ok
}

// ElementSize is the byte size of one element. It is 0 for BYTES and for an unknown datatype.
func (d Datatype) ElementSize() int {
	return elementSizes[d]
}

// JSONInput reports whether an input of this datatype may be sent as JSON values.
func (d Datatype) JSONInput() bool {
	return d.Known() && d != TypeFP16 && d != TypeBF16
}

// BinaryOutputOnly reports whether an output of this datatype is always returned as binary.
// Values of these types are not exact, or have no portable form, as JSON numbers.
func (d Datatype) BinaryOutputOnly() bool {
	switch d {
	case TypeInt64, TypeUint64, TypeFP16, TypeBF16:
		return true
	default:
		return false
	}
}
