//go:build !cgo

package ort

import (
	"context"
	"errors"
)

// errUnavailable is returned when the process was built without the runtime library.
var errUnavailable = errors.New("onnx runtime library is unavailable")

// Init reports that the shared library cannot be loaded.
func Init() error {
	return errUnavailable
}

// GetVersion reports that no shared library is loaded.
func GetVersion() (string, error) {
	return "", errUnavailable
}

// AvailableProviders reports that no shared library is loaded.
func AvailableProviders() ([]string, error) {
	return nil, errUnavailable
}

// Shutdown reports that no environment was started.
func Shutdown() error {
	return nil
}

// Session is one loaded model. Without the shared library, Open never returns one.
type Session struct{}

// Open reports that a session cannot be created.
func Open(modelPath string, intraOp, interOp int, providers []Provider) (*Session, error) {
	return nil, errUnavailable
}

// Inputs reports no tensors.
func (s *Session) Inputs() []TensorInfo {
	return nil
}

// Outputs reports no tensors.
func (s *Session) Outputs() []TensorInfo {
	return nil
}

// Run reports that the session cannot execute.
func (s *Session) Run(ctx context.Context, inputs map[string]*Tensor) (map[string]*Tensor, error) {
	return nil, errUnavailable
}

// Close releases nothing.
func (s *Session) Close() error {
	return nil
}

// Tensor is a numeric tensor. Without the shared library, NewTensor never returns one.
type Tensor struct{}

// NewTensor reports that a tensor cannot be created.
func NewTensor[T TensorElement](shape []int64, data []T) (*Tensor, error) {
	return nil, errUnavailable
}

// TensorData reports that the tensor cannot be read.
func TensorData[T TensorElement](tensor *Tensor) ([]T, error) {
	return nil, errUnavailable
}

// Shape reports no shape.
func (t *Tensor) Shape() []int64 {
	return nil
}

// ElementType reports an undefined element type.
func (t *Tensor) ElementType() ElementType {
	return ElementUndefined
}

// Bytes reports that the tensor cannot be read.
func (t *Tensor) Bytes() ([]byte, error) {
	return nil, errUnavailable
}

// Close releases nothing.
func (t *Tensor) Close() error {
	return nil
}
