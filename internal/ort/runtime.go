//go:build cgo

package ort

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/microsoft/onnxruntime/go/onnxruntime"
)

var (
	errSessionClosed = errors.New("run session: session is closed")
	errTensorClosed  = errors.New("read tensor: tensor is closed")
)

// openSessions counts sessions this package still has open.
// Shutdown refuses to run while the count is above zero.
var (
	envMu        sync.Mutex
	openSessions int
)

// Init loads the shared library from ORT_LIB_PATH and starts one environment.
// A second call after success succeeds. A failed call leaves the environment
// uninitialized and may be retried. After Shutdown, Init fails.
func Init() error {
	if err := onnxruntime.Init(); err != nil {
		return fmt.Errorf("initialize onnx runtime: %w", err)
	}
	return nil
}

// GetVersion returns the loaded shared library version.
// It is available after Init succeeds.
func GetVersion() (string, error) {
	version, err := onnxruntime.GetVersion()
	if err != nil {
		return "", fmt.Errorf("read onnx runtime version: %w", err)
	}
	return version, nil
}

// Shutdown releases the environment when no session from this package is open.
// A later Init fails. Shutdown with a session still open returns an error and
// leaves the environment running.
func Shutdown() error {
	envMu.Lock()
	defer envMu.Unlock()
	if openSessions > 0 {
		return fmt.Errorf("shut down onnx runtime: %d open sessions", openSessions)
	}
	if err := onnxruntime.Shutdown(); err != nil {
		return fmt.Errorf("shut down onnx runtime: %w", err)
	}
	return nil
}

// Session is one loaded model.
type Session struct {
	mu     sync.Mutex
	inner  *onnxruntime.Session
	closed bool
}

// AvailableProviders returns the execution providers built into the loaded library.
// It is available after Init succeeds.
func AvailableProviders() ([]string, error) {
	providers, err := onnxruntime.AvailableProviders()
	if err != nil {
		return nil, fmt.Errorf("read execution providers: %w", err)
	}
	return providers, nil
}

// Open loads one model into a session.
// intraOp and interOp are passed through, including zero, which selects the runtime default.
// providers are appended in order. The first failure closes the options and returns the error.
// Nothing further is appended. An empty chain is an error. A provider or file error returns no session.
func Open(modelPath string, intraOp, interOp int, providers []Provider) (session *Session, err error) {
	if len(providers) == 0 {
		return nil, errors.New("append execution provider: empty chain")
	}
	opts, err := onnxruntime.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("create session options: %w", err)
	}
	defer func() {
		if cerr := opts.Close(); cerr != nil && err == nil {
			if session != nil {
				_ = session.Close()
				session = nil
			}
			err = fmt.Errorf("close session options: %w", cerr)
		}
	}()

	if err = opts.SetIntraOpNumThreads(intraOp); err != nil {
		return nil, fmt.Errorf("set intra-op threads: %w", err)
	}
	if err = opts.SetInterOpNumThreads(interOp); err != nil {
		return nil, fmt.Errorf("set inter-op threads: %w", err)
	}
	for _, provider := range providers {
		if err = opts.AppendExecutionProvider(provider.Name, provider.Options); err != nil {
			return nil, fmt.Errorf("append execution provider: %w", err)
		}
	}

	inner, createErr := onnxruntime.NewSession(modelPath, opts)
	if createErr != nil {
		return nil, fmt.Errorf("create session: %w", createErr)
	}

	trackSession()
	session = &Session{inner: inner}
	return session, nil
}

// Inputs returns the model inputs. A negative shape length is a dynamic dimension.
func (s *Session) Inputs() []TensorInfo {
	if s == nil || s.inner == nil {
		return nil
	}
	return convertInfos(s.inner.Inputs())
}

// Outputs returns the model outputs. A negative shape length is a dynamic dimension.
func (s *Session) Outputs() []TensorInfo {
	if s == nil || s.inner == nil {
		return nil
	}
	return convertInfos(s.inner.Outputs())
}

// Run executes the session.
// Canceling ctx asks the runtime to stop the run.
// The caller closes input tensors and the returned output tensors.
func (s *Session) Run(ctx context.Context, inputs map[string]*Tensor) (map[string]*Tensor, error) {
	if s == nil || s.inner == nil {
		return nil, errSessionClosed
	}
	innerInputs := make(map[string]*onnxruntime.Tensor, len(inputs))
	for name, tensor := range inputs {
		if tensor == nil || tensor.inner == nil {
			return nil, fmt.Errorf("run session: input %s has no tensor", name)
		}
		innerInputs[name] = tensor.inner
	}
	outputs, err := s.inner.Run(ctx, innerInputs, nil)
	if err != nil {
		return nil, fmt.Errorf("run session: %w", err)
	}
	result := make(map[string]*Tensor, len(outputs))
	for name, tensor := range outputs {
		result[name] = &Tensor{inner: tensor}
	}
	return result, nil
}

// Close releases the session. It is safe to call more than once.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.inner == nil {
		return nil
	}
	err := s.inner.Close()
	untrackSession()
	if err != nil {
		return fmt.Errorf("close session: %w", err)
	}
	return nil
}

// Tensor is a numeric tensor. A tensor created from a Go slice keeps that slice
// until Close. The caller closes it, including after Run.
type Tensor struct {
	inner *onnxruntime.Tensor
}

// NewTensor creates a tensor backed by data.
// data must stay unchanged until the tensor is closed.
func NewTensor[T TensorElement](shape []int64, data []T) (*Tensor, error) {
	inner, err := onnxruntime.CreateTensor(shape, data)
	if err != nil {
		return nil, fmt.Errorf("create tensor: %w", err)
	}
	return &Tensor{inner: inner}, nil
}

// TensorData returns the tensor elements. The slice is valid until Close.
func TensorData[T TensorElement](tensor *Tensor) ([]T, error) {
	if tensor == nil || tensor.inner == nil {
		return nil, errTensorClosed
	}
	data, err := onnxruntime.TensorData[T](tensor.inner)
	if err != nil {
		return nil, fmt.Errorf("read tensor: %w", err)
	}
	return data, nil
}

// Shape returns a copy of the tensor shape.
func (t *Tensor) Shape() []int64 {
	if t == nil || t.inner == nil {
		return nil
	}
	return t.inner.Shape()
}

// ElementType returns the ONNX element type.
func (t *Tensor) ElementType() ElementType {
	if t == nil || t.inner == nil {
		return ElementUndefined
	}
	return ElementType(t.inner.DataType())
}

// Bytes returns a raw view of the tensor. The slice is valid until Close.
func (t *Tensor) Bytes() ([]byte, error) {
	if t == nil || t.inner == nil {
		return nil, errTensorClosed
	}
	data, err := t.inner.Bytes()
	if err != nil {
		return nil, fmt.Errorf("read tensor: %w", err)
	}
	return data, nil
}

// Close releases the tensor. It is safe to call more than once.
func (t *Tensor) Close() error {
	if t == nil || t.inner == nil {
		return nil
	}
	if err := t.inner.Close(); err != nil {
		return fmt.Errorf("close tensor: %w", err)
	}
	t.inner = nil
	return nil
}

func convertInfos(infos []onnxruntime.IOInfo) []TensorInfo {
	out := make([]TensorInfo, len(infos))
	for i, info := range infos {
		shape := make([]int64, len(info.Shape))
		copy(shape, info.Shape)
		out[i] = TensorInfo{
			Name:        info.Name,
			ElementType: ElementType(info.DataType),
			Shape:       shape,
		}
	}
	return out
}

func trackSession() {
	envMu.Lock()
	openSessions++
	envMu.Unlock()
}

func untrackSession() {
	envMu.Lock()
	if openSessions > 0 {
		openSessions--
	}
	envMu.Unlock()
}
