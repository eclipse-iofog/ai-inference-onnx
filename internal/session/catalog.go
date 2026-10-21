//go:build cgo

package session

import (
	"github.com/eclipse-iofog/ai-inference-onnx/internal/discover"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/ort"
)

// Catalog returns every model directory for metadata, the model list, and the repository index.
// A directory keeps its selected versions while it is loading and after a load fails.
// The signature is present when a session for that version is open.
func (r *Repository) Catalog() []CatalogModel {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.catalogLocked()
}

// catalogLocked copies the model list. The caller holds the repository lock.
func (r *Repository) catalogLocked() []CatalogModel {
	out := make([]CatalogModel, 0, len(r.order))
	for _, name := range r.order {
		item := r.models[name]
		if item == nil {
			continue
		}
		out = append(out, item.catalog())
	}
	return out
}

func (m *loadedModel) catalog() CatalogModel {
	view := CatalogModel{
		Name:                   m.name,
		State:                  m.state,
		Reason:                 m.reason,
		QueueDelayMicroseconds: m.queueDelay,
		QueueDelaySet:          m.queueDelaySet,
	}
	names := m.selected
	if len(names) == 0 {
		names = make([]string, 0, len(m.versions))
		for _, version := range m.versions {
			if version != nil {
				names = append(names, version.version)
			}
		}
	}
	open := make(map[string]*versionSession, len(m.versions))
	for _, version := range m.versions {
		if version != nil {
			open[version.version] = version
		}
	}
	view.Versions = make([]CatalogVersion, 0, len(names))
	for _, name := range names {
		version := CatalogVersion{Version: name}
		if running := open[name]; running != nil {
			version.Inputs = catalogTensors(running.inputs)
			version.Outputs = catalogTensors(running.outputs)
			version.MaxBatchSize = running.maxBatch
		}
		view.Versions = append(view.Versions, version)
	}
	return view
}

func selectedNames(model discover.Model) []string {
	if len(model.Versions) == 0 {
		return nil
	}
	names := make([]string, len(model.Versions))
	for i, version := range model.Versions {
		names[i] = version.Version
	}
	return names
}

func catalogTensors(infos []ort.TensorInfo) []CatalogTensor {
	out := make([]CatalogTensor, len(infos))
	for i, info := range infos {
		out[i] = CatalogTensor{
			Name:     info.Name,
			Datatype: wireDatatype(info.ElementType),
			Shape:    wireShape(info.Shape),
		}
	}
	return out
}

func wireShape(shape []int64) []int64 {
	if len(shape) == 0 {
		return []int64{}
	}
	out := make([]int64, len(shape))
	for i, dim := range shape {
		if dim < 0 {
			out[i] = -1
			continue
		}
		out[i] = dim
	}
	return out
}

func wireDatatype(element ort.ElementType) string {
	switch element {
	case ort.ElementFloat32:
		return "FP32"
	case ort.ElementUint8:
		return "UINT8"
	case ort.ElementInt8:
		return "INT8"
	case ort.ElementUint16:
		return "UINT16"
	case ort.ElementInt16:
		return "INT16"
	case ort.ElementInt32:
		return "INT32"
	case ort.ElementInt64:
		return "INT64"
	case ort.ElementString:
		return "BYTES"
	case ort.ElementBool:
		return "BOOL"
	case ort.ElementFloat16:
		return "FP16"
	case ort.ElementFloat64:
		return "FP64"
	case ort.ElementUint32:
		return "UINT32"
	case ort.ElementUint64:
		return "UINT64"
	case ort.ElementBFloat16:
		return "BF16"
	default:
		return ""
	}
}
