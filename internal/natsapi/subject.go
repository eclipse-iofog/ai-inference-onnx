// Package natsapi serves inference calls that arrive as NATS subjects.
package natsapi

import (
	"errors"
	"fmt"
	"strings"
)

// Operation is the call a subject names.
type Operation int

// Operations a subject can name. The zero value is not a call.
const (
	OpLive Operation = iota + 1
	OpReady
	OpServerMetadata
	OpModelList
	OpRepositoryIndex
	OpModelMetadata
	OpModelReady
	OpModelInfer
)

// String returns a short name for logs.
func (o Operation) String() string {
	switch o {
	case OpLive:
		return "health live"
	case OpReady:
		return "health ready"
	case OpServerMetadata:
		return "server metadata"
	case OpModelList:
		return "model list"
	case OpRepositoryIndex:
		return "repository index"
	case OpModelMetadata:
		return "model metadata"
	case OpModelReady:
		return "model ready"
	case OpModelInfer:
		return "model infer"
	default:
		return "unknown"
	}
}

// Subject is a parsed request subject.
type Subject struct {
	// Op is the call the subject names.
	Op Operation
	// Model is the model name for model calls. It may contain dots.
	Model string
	// Version is the requested version. Empty means the server chooses.
	// It is not checked against the repository here.
	Version string
}

var (
	// ErrWildcard is a subject whose tokens contain a NATS wildcard character.
	ErrWildcard = errors.New("subject contains a wildcard character")
	// ErrUnknownSubject is a subject that names no call this server serves.
	ErrUnknownSubject = errors.New("subject names no inference call")
)

const (
	subjectPrefix  = "inference.v2."
	versionsToken  = "versions"
	modelsToken    = "models"
	wildcardTokens = "*>"
)

// fixedSubjects are the subjects that carry no model name.
var fixedSubjects = map[string]Operation{
	"inference.v2.health.live":      OpLive,
	"inference.v2.health.ready":     OpReady,
	"inference.v2.server.metadata":  OpServerMetadata,
	"inference.v2.models":           OpModelList,
	"inference.v2.repository.index": OpRepositoryIndex,
}

// modelOperations are the last token of a model subject.
var modelOperations = map[string]Operation{
	"metadata": OpModelMetadata,
	"ready":    OpModelReady,
	"infer":    OpModelInfer,
}

// ParseSubject reads a request subject.
// A model name may contain dots. When the third token from the end is
// "versions", the token after it is the version and the tokens before it are the name.
// It returns ErrWildcard or ErrUnknownSubject, wrapped, for a subject it does not serve.
func ParseSubject(subject string) (Subject, error) {
	if strings.ContainsAny(subject, wildcardTokens) {
		return Subject{}, fmt.Errorf("%w: %q", ErrWildcard, subject)
	}
	if op, ok := fixedSubjects[subject]; ok {
		return Subject{Op: op}, nil
	}

	tokens := strings.Split(subject, ".")
	for _, token := range tokens {
		if token == "" {
			return Subject{}, fmt.Errorf("%w: %q has an empty token", ErrUnknownSubject, subject)
		}
	}
	if !strings.HasPrefix(subject, subjectPrefix) || len(tokens) < 5 || tokens[2] != modelsToken {
		return Subject{}, fmt.Errorf("%w: %q", ErrUnknownSubject, subject)
	}

	last := len(tokens) - 1
	op, ok := modelOperations[tokens[last]]
	if !ok {
		return Subject{}, fmt.Errorf("%w: %q", ErrUnknownSubject, subject)
	}

	nameTokens := tokens[3:last]
	version := ""
	if versionsAt := last - 2; versionsAt >= 3 && tokens[versionsAt] == versionsToken {
		nameTokens = tokens[3:versionsAt]
		version = tokens[last-1]
	}
	if len(nameTokens) == 0 {
		return Subject{}, fmt.Errorf("%w: %q has an empty model name", ErrUnknownSubject, subject)
	}
	return Subject{Op: op, Model: strings.Join(nameTokens, "."), Version: version}, nil
}
