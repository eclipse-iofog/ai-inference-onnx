package natsapi

import (
	"errors"
	"strings"
	"testing"
)

// FuzzParseSubject checks that a subject is parsed or refused without crashing.
func FuzzParseSubject(f *testing.F) {
	f.Add("inference.v2.health.live")
	f.Add("inference.v2.models.my.detector.versions.2.infer")
	f.Add("inference.v2.models.wafer-defect.infer")
	f.Add("inference.v2.models.a*b.infer")
	f.Add("inference.v2.models.a>b.infer")
	f.Add("inference.v2.repository.models.add.load")
	f.Add("")
	f.Add("inference.v2.models.versions.1.infer")

	f.Fuzz(func(t *testing.T, subject string) {
		got, err := ParseSubject(subject)
		if err != nil {
			if !errors.Is(err, ErrWildcard) && !errors.Is(err, ErrUnknownSubject) {
				t.Fatalf("ParseSubject(%q) returned an unclassified error: %v", subject, err)
			}
			if got != (Subject{}) {
				t.Fatalf("ParseSubject(%q) returned %+v with an error", subject, got)
			}
			return
		}
		if strings.ContainsAny(subject, "*>") {
			t.Fatalf("ParseSubject(%q) accepted a wildcard", subject)
		}
		if got.Op < OpLive || got.Op > OpModelInfer {
			t.Fatalf("ParseSubject(%q) returned operation %d", subject, got.Op)
		}
		if err := checkModelFields(subject, got); err != nil {
			t.Fatal(err)
		}
	})
}

// checkModelFields rebuilds the subject from the parsed value and compares it.
func checkModelFields(subject string, got Subject) error {
	switch got.Op {
	case OpModelMetadata, OpModelReady, OpModelInfer:
	default:
		if got.Model != "" || got.Version != "" {
			return errors.New("a call without a model carries a model or version")
		}
		return nil
	}
	if got.Model == "" {
		return errors.New("a model call has an empty name")
	}
	rebuilt := "inference.v2.models." + got.Model
	if got.Version != "" {
		rebuilt += ".versions." + got.Version
	}
	rebuilt += "." + operationToken(got.Op)
	if rebuilt != subject {
		return errors.New("parsed fields do not rebuild " + subject + ", got " + rebuilt)
	}
	return nil
}

func operationToken(op Operation) string {
	for token, candidate := range modelOperations {
		if candidate == op {
			return token
		}
	}
	return ""
}
