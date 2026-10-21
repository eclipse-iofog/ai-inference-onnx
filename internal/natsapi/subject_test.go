package natsapi

import (
	"errors"
	"testing"
)

func TestParseSubjectAccepts(t *testing.T) {
	tests := []struct {
		subject string
		want    Subject
	}{
		{"inference.v2.health.live", Subject{Op: OpLive}},
		{"inference.v2.health.ready", Subject{Op: OpReady}},
		{"inference.v2.server.metadata", Subject{Op: OpServerMetadata}},
		{"inference.v2.models", Subject{Op: OpModelList}},
		{"inference.v2.repository.index", Subject{Op: OpRepositoryIndex}},
		{"inference.v2.models.add.metadata", Subject{Op: OpModelMetadata, Model: "add"}},
		{"inference.v2.models.add.ready", Subject{Op: OpModelReady, Model: "add"}},
		{"inference.v2.models.add.infer", Subject{Op: OpModelInfer, Model: "add"}},
		{"inference.v2.models.add.versions.1.metadata", Subject{Op: OpModelMetadata, Model: "add", Version: "1"}},
		{"inference.v2.models.add.versions.1.ready", Subject{Op: OpModelReady, Model: "add", Version: "1"}},
		{"inference.v2.models.add.versions.1.infer", Subject{Op: OpModelInfer, Model: "add", Version: "1"}},
		{"inference.v2.models.my.detector.versions.2.infer", Subject{Op: OpModelInfer, Model: "my.detector", Version: "2"}},
		{"inference.v2.models.my.detector.infer", Subject{Op: OpModelInfer, Model: "my.detector"}},
		{"inference.v2.models.wafer-defect.infer", Subject{Op: OpModelInfer, Model: "wafer-defect"}},
		{"inference.v2.models.wafer-defect.versions.1.infer", Subject{Op: OpModelInfer, Model: "wafer-defect", Version: "1"}},
		{"inference.v2.models.foo.versions.infer", Subject{Op: OpModelInfer, Model: "foo.versions"}},
		{"inference.v2.models.foo.versions.ready", Subject{Op: OpModelReady, Model: "foo.versions"}},
		{"inference.v2.models.add.versions.latest.infer", Subject{Op: OpModelInfer, Model: "add", Version: "latest"}},
		{"inference.v2.models.add.versions.v1.ready", Subject{Op: OpModelReady, Model: "add", Version: "v1"}},
		{"inference.v2.models.add.versions.01.metadata", Subject{Op: OpModelMetadata, Model: "add", Version: "01"}},
	}
	for _, tt := range tests {
		t.Run(tt.subject, func(t *testing.T) {
			got, err := ParseSubject(tt.subject)
			if err != nil {
				t.Fatalf("ParseSubject: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseSubject = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseSubjectFlatModelWithAndWithoutVersion(t *testing.T) {
	flat, err := ParseSubject("inference.v2.models.wafer-defect.infer")
	if err != nil {
		t.Fatal(err)
	}
	versioned, err := ParseSubject("inference.v2.models.wafer-defect.versions.1.infer")
	if err != nil {
		t.Fatal(err)
	}
	if flat.Model != versioned.Model || flat.Op != versioned.Op {
		t.Fatalf("flat %+v and versioned %+v name different calls", flat, versioned)
	}
}

func TestParseSubjectRejectsWildcards(t *testing.T) {
	subjects := []string{
		"inference.v2.models.*.infer",
		"inference.v2.models.>",
		"inference.v2.models.a*b.infer",
		"inference.v2.models.a>b.infer",
		"inference.v2.models.add.versions.*.infer",
		"inference.v2.models.add.versions.1>.infer",
		"inference.v2.models.my.*.versions.1.ready",
		"inference.v2.health.*",
		"inference.>",
		">",
		"*",
	}
	for _, subject := range subjects {
		t.Run(subject, func(t *testing.T) {
			_, err := ParseSubject(subject)
			if !errors.Is(err, ErrWildcard) {
				t.Fatalf("ParseSubject err = %v, want wildcard error", err)
			}
		})
	}
}

func TestParseSubjectRejectsUnknown(t *testing.T) {
	subjects := []string{
		"",
		".",
		"inference",
		"inference.v2",
		"inference.v2.",
		"inference.v2.health",
		"inference.v2.health.live.extra",
		"inference.v2.server",
		"inference.v2.models.",
		"inference.v2.models.add",
		"inference.v2.models.add.",
		"inference.v2.models..infer",
		"inference.v2.models.my..detector.infer",
		"inference.v2.models.versions.1.infer",
		"inference.v2.models.versions.1.ready",
		"inference.v2.models.add.versions..infer",
		"inference.v2.models.add.load",
		"inference.v2.models.add.unload",
		"inference.v2.models.add.versions.1.load",
		"inference.v2.repository.models.add.load",
		"inference.v2.repository.models.add.unload",
		"inference.v2.repository.index.extra",
		"inference.v2.model.add.infer",
		"inference.v1.models.add.infer",
		"other.v2.models.add.infer",
		"inference.xfer.0123456789abcdef0123456789abcdef",
		"inference.metrics.engines.e1",
		".inference.v2.models.add.infer",
		"inference.v2.models.add.infer.",
		"inference.v2.models.add.INFER",
	}
	for _, subject := range subjects {
		t.Run(subject, func(t *testing.T) {
			got, err := ParseSubject(subject)
			if !errors.Is(err, ErrUnknownSubject) {
				t.Fatalf("ParseSubject = %+v, %v; want unknown subject error", got, err)
			}
			if got != (Subject{}) {
				t.Fatalf("ParseSubject returned %+v with an error", got)
			}
		})
	}
}

func TestOperationString(t *testing.T) {
	for op := OpLive; op <= OpModelInfer; op++ {
		if op.String() == "unknown" {
			t.Fatalf("operation %d has no name", op)
		}
	}
	if Operation(0).String() != "unknown" {
		t.Fatal("zero operation should be unknown")
	}
}
