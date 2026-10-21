package ort

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSelectProviders(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "cache")
	trtDir := filepath.Join(cache, "inference", "tensorrt")
	trt := Provider{
		Name: TensorrtExecutionProvider,
		Options: map[string]string{
			"trt_engine_cache_enable": "1",
			"trt_timing_cache_enable": "1",
			"trt_engine_cache_path":   trtDir,
			"trt_timing_cache_path":   trtDir,
		},
	}
	trtNoCache := Provider{Name: TensorrtExecutionProvider}
	cuda := Provider{Name: CUDAExecutionProvider}
	cpu := Provider{Name: CPUExecutionProvider}
	migraphx := Provider{Name: MIGraphXExecutionProvider}
	openvino := Provider{Name: OpenVINOExecutionProvider}
	qnn := Provider{Name: QNNExecutionProvider, Options: map[string]string{"backend_type": "htp"}}

	tests := []struct {
		name      string
		available []string
		cacheRoot string
		want      []Provider
	}{
		{
			name:      "tensorrt",
			available: []string{TensorrtExecutionProvider, CUDAExecutionProvider, OpenVINOExecutionProvider, CPUExecutionProvider},
			cacheRoot: cache,
			want:      []Provider{trt, cuda},
		},
		{
			name:      "tensorrt without cuda",
			available: []string{TensorrtExecutionProvider},
			cacheRoot: cache,
			want:      []Provider{trt, cuda},
		},
		{
			name:      "tensorrt empty cache",
			available: []string{TensorrtExecutionProvider, CUDAExecutionProvider},
			cacheRoot: "",
			want:      []Provider{trtNoCache, cuda},
		},
		{
			name:      "cuda",
			available: []string{OpenVINOExecutionProvider, QNNExecutionProvider, CUDAExecutionProvider},
			cacheRoot: cache,
			want:      []Provider{cuda},
		},
		{
			name:      "migraphx",
			available: []string{MIGraphXExecutionProvider, OpenVINOExecutionProvider, CPUExecutionProvider},
			cacheRoot: cache,
			want:      []Provider{migraphx},
		},
		{
			name:      "openvino",
			available: []string{QNNExecutionProvider, OpenVINOExecutionProvider},
			cacheRoot: cache,
			want:      []Provider{openvino},
		},
		{
			name:      "qnn",
			available: []string{QNNExecutionProvider, CPUExecutionProvider},
			cacheRoot: cache,
			want:      []Provider{qnn},
		},
		{
			name:      "cpu only",
			available: []string{CPUExecutionProvider},
			cacheRoot: cache,
			want:      []Provider{cpu},
		},
		{
			name:      "empty",
			available: nil,
			cacheRoot: cache,
			want:      []Provider{cpu},
		},
		{
			name:      "xnnpack",
			available: []string{"XnnpackExecutionProvider"},
			cacheRoot: cache,
			want:      []Provider{cpu},
		},
		{
			name:      "dnnl",
			available: []string{"DnnlExecutionProvider"},
			cacheRoot: cache,
			want:      []Provider{cpu},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := SelectProviders(test.available, test.cacheRoot)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("chain %+v, want %+v", got, test.want)
			}
		})
	}
}
