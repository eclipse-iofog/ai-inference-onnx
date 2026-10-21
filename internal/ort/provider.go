package ort

import "path/filepath"

const (
	// CUDAExecutionProvider is the CUDA execution provider.
	CUDAExecutionProvider = "CUDAExecutionProvider"
	// TensorrtExecutionProvider is the TensorRT execution provider.
	TensorrtExecutionProvider = "TensorrtExecutionProvider"
	// MIGraphXExecutionProvider is the MIGraphX execution provider.
	MIGraphXExecutionProvider = "MIGraphXExecutionProvider"
	// OpenVINOExecutionProvider is the OpenVINO execution provider.
	OpenVINOExecutionProvider = "OpenVINOExecutionProvider"
	// QNNExecutionProvider is the QNN execution provider.
	QNNExecutionProvider = "QNNExecutionProvider"
)

// Provider is one execution provider and the options appended with it.
type Provider struct {
	// Name is the provider name reported by the loaded library.
	Name string
	// Options are provider settings. Nil means the provider is appended with no options.
	Options map[string]string
}

// SelectProviders returns the provider chain for the names the loaded library reports.
// The first matching row wins. Names outside that list are ignored.
// An empty cacheRoot leaves the TensorRT cache options off.
func SelectProviders(available []string, cacheRoot string) []Provider {
	have := make(map[string]struct{}, len(available))
	for _, name := range available {
		have[name] = struct{}{}
	}
	if _, ok := have[TensorrtExecutionProvider]; ok {
		return []Provider{tensorRTProvider(cacheRoot), {Name: CUDAExecutionProvider}}
	}
	if _, ok := have[CUDAExecutionProvider]; ok {
		return []Provider{{Name: CUDAExecutionProvider}}
	}
	if _, ok := have[MIGraphXExecutionProvider]; ok {
		return []Provider{{Name: MIGraphXExecutionProvider}}
	}
	if _, ok := have[OpenVINOExecutionProvider]; ok {
		return []Provider{{Name: OpenVINOExecutionProvider}}
	}
	if _, ok := have[QNNExecutionProvider]; ok {
		return []Provider{{
			Name:    QNNExecutionProvider,
			Options: map[string]string{"backend_type": "htp"},
		}}
	}
	return []Provider{{Name: CPUExecutionProvider}}
}

func tensorRTProvider(cacheRoot string) Provider {
	provider := Provider{Name: TensorrtExecutionProvider}
	if cacheRoot == "" {
		return provider
	}
	dir := filepath.Join(cacheRoot, "inference", "tensorrt")
	provider.Options = map[string]string{
		"trt_engine_cache_enable": "1",
		"trt_timing_cache_enable": "1",
		"trt_engine_cache_path":   dir,
		"trt_timing_cache_path":   dir,
	}
	return provider
}
