package config

import (
	"strings"
	"testing"
)

func TestParseModelFileAbsent(t *testing.T) {
	for _, file := range [][]byte{nil, {}} {
		got := ParseModelFile("detector", file)
		requireAvailable(t, got)
		requireNoOverrides(t, got)
	}
}

func TestParseModelFileOverrides(t *testing.T) {
	t.Run("filename", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`default_model_filename: "wafer-defect.onnx"`))
		requireAvailable(t, got)
		requireString(t, got.DefaultModelFilename, "wafer-defect.onnx")
		if got.MaxBatchSize != nil || got.VersionPolicy != nil || got.MaxQueueDelayMicroseconds != nil {
			t.Fatalf("unexpected overrides %+v", got)
		}
	})

	t.Run("batch size", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`max_batch_size: 8`))
		requireAvailable(t, got)
		requireInt(t, got.MaxBatchSize, 8)
		if got.VersionPolicy != nil {
			t.Fatal("version policy was set")
		}
	})

	t.Run("batch size zero", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`max_batch_size: 0`))
		requireAvailable(t, got)
		requireInt(t, got.MaxBatchSize, 0)
	})

	t.Run("latest versions", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`version_policy: { latest: { num_versions: 2}}`))
		requireAvailable(t, got)
		requirePolicy(t, got.VersionPolicy, VersionPolicy{
			Kind:        VersionPolicyLatest,
			NumVersions: 2,
		})
	})

	t.Run("all versions", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`version_policy: { all: {}}`))
		requireAvailable(t, got)
		requirePolicy(t, got.VersionPolicy, VersionPolicy{Kind: VersionPolicyAll})
	})

	t.Run("all versions without a colon", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`version_policy: { all { }}`))
		requireAvailable(t, got)
		requirePolicy(t, got.VersionPolicy, VersionPolicy{Kind: VersionPolicyAll})
	})

	t.Run("specific versions", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`version_policy: { specific: { versions: [1, 3]}}`))
		requireAvailable(t, got)
		requirePolicy(t, got.VersionPolicy, VersionPolicy{
			Kind:     VersionPolicySpecific,
			Versions: []int64{1, 3},
		})
	})

	t.Run("specific versions without spaces", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`version_policy: { specific: { versions: [1,3]}}`))
		requireAvailable(t, got)
		requirePolicy(t, got.VersionPolicy, VersionPolicy{
			Kind:     VersionPolicySpecific,
			Versions: []int64{1, 3},
		})
	})

	t.Run("queue delay", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`
dynamic_batching {
  max_queue_delay_microseconds: 100
}
`))
		requireAvailable(t, got)
		requireUint(t, got.MaxQueueDelayMicroseconds, 100)
		if got.MaxBatchSize != nil {
			t.Fatalf("batch size %d", *got.MaxBatchSize)
		}
	})

	t.Run("queue delay zero", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`dynamic_batching { max_queue_delay_microseconds: 0 }`))
		requireAvailable(t, got)
		requireUint(t, got.MaxQueueDelayMicroseconds, 0)
	})

	t.Run("together", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`
default_model_filename: "wafer-defect.onnx"
max_batch_size: 0
version_policy: { specific: { versions: [1, 3]}}
dynamic_batching {
  max_queue_delay_microseconds: 1500
}
`))
		requireAvailable(t, got)
		requireString(t, got.DefaultModelFilename, "wafer-defect.onnx")
		requireInt(t, got.MaxBatchSize, 0)
		requirePolicy(t, got.VersionPolicy, VersionPolicy{
			Kind:     VersionPolicySpecific,
			Versions: []int64{1, 3},
		})
		requireUint(t, got.MaxQueueDelayMicroseconds, 1500)
	})
}

func TestParseModelFileIgnoresUnusedFields(t *testing.T) {
	got := ParseModelFile("detector", []byte(`
# Preferred sizes, priority, and instance placement are not used.
name: "detector"
default_model_filename: "detector.onnx"
max_batch_size: 4
version_policy: { latest: { num_versions: 2}}
dynamic_batching {
  preferred_batch_size: [ 4, 8 ]
  max_queue_delay_microseconds: 100
  preserve_ordering: true
  priority_levels: 2
  default_priority_level: 1
}
instance_group [
  {
    count: 2
    kind: KIND_GPU
  }
]
input [
  {
    name: "input0"
    data_type: TYPE_FP32
    dims: [ 16 ]
  }
]
output [
  {
    name: "output0"
    data_type: TYPE_FP32
    dims: [ 16 ]
  }
]
`))
	requireAvailable(t, got)
	requireString(t, got.DefaultModelFilename, "detector.onnx")
	requireInt(t, got.MaxBatchSize, 4)
	requirePolicy(t, got.VersionPolicy, VersionPolicy{
		Kind:        VersionPolicyLatest,
		NumVersions: 2,
	})
	requireUint(t, got.MaxQueueDelayMicroseconds, 100)
}

func TestParseModelFileName(t *testing.T) {
	t.Run("matches the directory", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`
name: "detector"
default_model_filename: "detector.onnx"
`))
		requireAvailable(t, got)
		requireString(t, got.DefaultModelFilename, "detector.onnx")
	})

	t.Run("differs from the directory", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`name: "other"`))
		requireUnavailable(t, got)
	})

	t.Run("omitted", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`max_batch_size: 4`))
		requireAvailable(t, got)
		requireInt(t, got.MaxBatchSize, 4)
	})
}

func TestParseModelFilePlatformAndBackend(t *testing.T) {
	t.Run("wrong platform", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`platform: "tensorrt_plan"`))
		requireUnavailable(t, got)
	})

	t.Run("wrong backend", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`backend: "tensorrt"`))
		requireUnavailable(t, got)
	})

	t.Run("platform omitted", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`backend: "onnxruntime"`))
		requireAvailable(t, got)
		requireNoOverrides(t, got)
	})

	t.Run("backend omitted", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`platform: "onnxruntime_onnx"`))
		requireAvailable(t, got)
		requireNoOverrides(t, got)
	})

	t.Run("both onnx runtime", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`
name: "detector"
platform: "onnxruntime_onnx"
backend: "onnxruntime"
`))
		requireAvailable(t, got)
		requireNoOverrides(t, got)
	})

	t.Run("both omitted", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`default_model_filename: "detector.onnx"`))
		requireAvailable(t, got)
		requireString(t, got.DefaultModelFilename, "detector.onnx")
		if got.MaxBatchSize != nil {
			t.Fatalf("batch size %d", *got.MaxBatchSize)
		}
	})

	t.Run("both wrong", func(t *testing.T) {
		got := ParseModelFile("detector", []byte(`
platform: "tensorrt_plan"
backend: "tensorrt"
`))
		requireUnavailable(t, got)
		if !strings.Contains(got.Reason, "platform") || !strings.Contains(got.Reason, "backend") {
			t.Fatalf("reason %q", got.Reason)
		}
	})
}

func TestParseModelFileUnsupportedFields(t *testing.T) {
	cases := []struct {
		name   string
		file   string
		fields []string
	}{
		{
			name:   "sequence batching",
			file:   "sequence_batching { max_sequence_idle_microseconds: 100 }",
			fields: []string{"sequence_batching"},
		},
		{
			name: "ensemble scheduling",
			file: `
ensemble_scheduling {
  step [
    {
      model_name: "preprocess"
      model_version: -1
    }
  ]
}
`,
			fields: []string{"ensemble_scheduling"},
		},
		{
			name:   "response cache",
			file:   "response_cache { enable: false }",
			fields: []string{"response_cache"},
		},
		{
			name: "all three",
			file: `
sequence_batching { max_sequence_idle_microseconds: 100 }
ensemble_scheduling {
  step [
    {
      model_name: "preprocess"
      model_version: -1
    }
  ]
}
response_cache { enable: false }
`,
			fields: []string{"sequence_batching", "ensemble_scheduling", "response_cache"},
		},
	}
	all := []string{"sequence_batching", "ensemble_scheduling", "response_cache"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseModelFile("detector", []byte(tc.file))
			requireUnavailable(t, got)
			for _, field := range tc.fields {
				if !strings.Contains(got.Reason, field) {
					t.Fatalf("reason %q does not name %s", got.Reason, field)
				}
			}
			for _, field := range all {
				if contains(tc.fields, field) {
					continue
				}
				if strings.Contains(got.Reason, field) {
					t.Fatalf("reason %q names %s", got.Reason, field)
				}
			}
		})
	}
}

func TestParseModelFileNegativeBatchSize(t *testing.T) {
	got := ParseModelFile("detector", []byte(`
default_model_filename: "detector.onnx"
max_batch_size: -1
`))
	requireUnavailable(t, got)
	if strings.Contains(got.Reason, "could not be parsed") {
		t.Fatalf("reason %q", got.Reason)
	}
	if got.MaxBatchSize != nil {
		t.Fatalf("batch size %d", *got.MaxBatchSize)
	}
}

func TestParseModelFileUnparseable(t *testing.T) {
	nested := strings.Repeat("wrap { ", maxPBTXTDepth+2) + strings.Repeat("}", maxPBTXTDepth+2)
	cases := []string{
		"this is not protobuf text",
		"name: \"unterminated",
		`max_batch_size: "8"`,
		"version_policy: { latest: { num_versions: -1 }}",
		"{",
		"dynamic_batching { max_queue_delay_microseconds: 1",
		nested,
	}
	for _, file := range cases {
		t.Run(file, func(t *testing.T) {
			got := ParseModelFile("detector", []byte(file))
			if got.Available {
				t.Fatal("available")
			}
			if !strings.Contains(got.Reason, "could not be parsed") {
				t.Fatalf("reason %q", got.Reason)
			}
		})
	}
}

func requireAvailable(t *testing.T, got ModelFile) {
	t.Helper()
	if !got.Available {
		t.Fatalf("unavailable: %s", got.Reason)
	}
	if got.Reason != "" {
		t.Fatalf("reason %q", got.Reason)
	}
}

func requireUnavailable(t *testing.T, got ModelFile) {
	t.Helper()
	if got.Available {
		t.Fatal("available")
	}
	if got.Reason == "" {
		t.Fatal("empty reason")
	}
}

func requireNoOverrides(t *testing.T, got ModelFile) {
	t.Helper()
	if got.DefaultModelFilename != nil || got.MaxBatchSize != nil || got.VersionPolicy != nil || got.MaxQueueDelayMicroseconds != nil {
		t.Fatalf("filename %v batch %v policy %v delay %v", got.DefaultModelFilename, got.MaxBatchSize, got.VersionPolicy, got.MaxQueueDelayMicroseconds)
	}
}

func requireString(t *testing.T, got *string, want string) {
	t.Helper()
	if got == nil {
		t.Fatal("missing string")
	}
	if *got != want {
		t.Fatalf("got %q want %q", *got, want)
	}
}

func requireInt(t *testing.T, got *int, want int) {
	t.Helper()
	if got == nil {
		t.Fatal("missing int")
	}
	if *got != want {
		t.Fatalf("got %d want %d", *got, want)
	}
}

func requireUint(t *testing.T, got *uint64, want uint64) {
	t.Helper()
	if got == nil {
		t.Fatal("missing uint")
	}
	if *got != want {
		t.Fatalf("got %d want %d", *got, want)
	}
}

func requirePolicy(t *testing.T, got *VersionPolicy, want VersionPolicy) {
	t.Helper()
	if got == nil {
		t.Fatal("missing version policy")
	}
	if got.Kind != want.Kind || got.NumVersions != want.NumVersions || len(got.Versions) != len(want.Versions) {
		t.Fatalf("got %+v want %+v", *got, want)
	}
	for i := range want.Versions {
		if got.Versions[i] != want.Versions[i] {
			t.Fatalf("got %+v want %+v", *got, want)
		}
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
