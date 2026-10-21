package config

import (
	"fmt"
	"math"
	"strings"
)

const (
	// PlatformONNXRuntime is the platform name for an ONNX Runtime model.
	PlatformONNXRuntime = "onnxruntime_onnx"
	// BackendONNXRuntime is the backend name for an ONNX Runtime model.
	BackendONNXRuntime = "onnxruntime"

	// VersionPolicyLatest keeps the newest N versions.
	VersionPolicyLatest = "latest"
	// VersionPolicyAll keeps every version in the model directory.
	VersionPolicyAll = "all"
	// VersionPolicySpecific keeps an explicit version list.
	VersionPolicySpecific = "specific"
)

// ModelFile is the subset of a Triton config.pbtxt this engine honors.
type ModelFile struct {
	// Available is false when the file makes the model unavailable.
	Available bool
	// Reason explains why the model is unavailable. It is empty when Available is true.
	Reason string
	// DefaultModelFilename is the ONNX file named by the config. Nil when the file omits it.
	DefaultModelFilename *string
	// MaxBatchSize is the batch cap from the config, including 0. Nil when the file omits it.
	MaxBatchSize *int
	// VersionPolicy is the version selection from the config. Nil when the file omits it.
	VersionPolicy *VersionPolicy
	// MaxQueueDelayMicroseconds is the per-model queue delay, including 0. Nil when the file omits it.
	MaxQueueDelayMicroseconds *uint64
}

// VersionPolicy selects which numeric versions of a model are used.
type VersionPolicy struct {
	// Kind is latest, all, or specific.
	Kind string
	// NumVersions is how many of the newest versions to keep when Kind is latest.
	NumVersions uint32
	// Versions lists the versions to keep when Kind is specific.
	Versions []int64
}

// ParseModelFile interprets one model's config.pbtxt.
// directoryName is the model directory's name, and file is that file's bytes.
// Nil or empty bytes mean the file is absent, which yields no overrides.
// The batch cap is left unset when the file omits it.
// ParseModelFile does not read the model directory or an ONNX file.
func ParseModelFile(directoryName string, file []byte) ModelFile {
	if len(file) == 0 {
		return ModelFile{Available: true}
	}
	fields, err := parsePBTXT(file)
	if err != nil {
		return unparsedModelFile()
	}
	parsed, err := interpretModelFile(directoryName, fields)
	if err != nil {
		return unparsedModelFile()
	}
	return parsed
}

func unparsedModelFile() ModelFile {
	return ModelFile{Available: false, Reason: "config.pbtxt could not be parsed"}
}

func interpretModelFile(directoryName string, fields []field) (ModelFile, error) {
	out := ModelFile{Available: true}
	var reasons []string

	if vals := findField(fields, "name"); vals != nil {
		text, err := oneString(vals)
		if err != nil {
			return ModelFile{}, err
		}
		if text != directoryName {
			reasons = append(reasons, fmt.Sprintf("name %q does not match the directory name %q", text, directoryName))
		}
	}
	if vals := findField(fields, "platform"); vals != nil {
		text, err := oneString(vals)
		if err != nil {
			return ModelFile{}, err
		}
		if text != PlatformONNXRuntime {
			reasons = append(reasons, fmt.Sprintf("platform %q is not %s", text, PlatformONNXRuntime))
		}
	}
	if vals := findField(fields, "backend"); vals != nil {
		text, err := oneString(vals)
		if err != nil {
			return ModelFile{}, err
		}
		if text != BackendONNXRuntime {
			reasons = append(reasons, fmt.Sprintf("backend %q is not %s", text, BackendONNXRuntime))
		}
	}
	if vals := findField(fields, "default_model_filename"); vals != nil {
		text, err := oneString(vals)
		if err != nil {
			return ModelFile{}, err
		}
		out.DefaultModelFilename = newString(text)
	}
	if vals := findField(fields, "max_batch_size"); vals != nil {
		n, err := oneInt(vals, 32)
		if err != nil {
			return ModelFile{}, err
		}
		if n < 0 {
			reasons = append(reasons, "max_batch_size must not be negative")
		} else {
			out.MaxBatchSize = newInt(int(n))
		}
	}
	if vals := findField(fields, "version_policy"); vals != nil {
		msg, err := oneMessage(vals)
		if err != nil {
			return ModelFile{}, err
		}
		policy, err := versionPolicy(msg)
		if err != nil {
			return ModelFile{}, err
		}
		out.VersionPolicy = policy
	}
	if vals := findField(fields, "dynamic_batching"); vals != nil {
		msg, err := oneMessage(vals)
		if err != nil {
			return ModelFile{}, err
		}
		if delays := findField(msg, "max_queue_delay_microseconds"); delays != nil {
			n, err := oneUint(delays, 64)
			if err != nil {
				return ModelFile{}, err
			}
			out.MaxQueueDelayMicroseconds = newUint64(n)
		}
	}

	unsupported, err := unsupportedFields(fields)
	if err != nil {
		return ModelFile{}, err
	}
	if len(unsupported) > 0 {
		reasons = append(reasons, unsupportedReason(unsupported))
	}
	if len(reasons) > 0 {
		return ModelFile{Available: false, Reason: strings.Join(reasons, "; ")}, nil
	}
	return out, nil
}

func versionPolicy(fields []field) (*VersionPolicy, error) {
	latest := findField(fields, "latest")
	all := findField(fields, "all")
	specific := findField(fields, "specific")
	set := 0
	if latest != nil {
		set++
	}
	if all != nil {
		set++
	}
	if specific != nil {
		set++
	}
	if set > 1 {
		return nil, errBadText
	}
	if set == 0 {
		return nil, nil
	}
	if all != nil {
		if _, err := oneMessage(all); err != nil {
			return nil, err
		}
		return &VersionPolicy{Kind: VersionPolicyAll}, nil
	}
	if latest != nil {
		msg, err := oneMessage(latest)
		if err != nil {
			return nil, err
		}
		policy := &VersionPolicy{Kind: VersionPolicyLatest}
		if nums := findField(msg, "num_versions"); nums != nil {
			n, err := oneUint(nums, 32)
			if err != nil {
				return nil, err
			}
			if n > math.MaxUint32 {
				return nil, errBadText
			}
			policy.NumVersions = uint32(n)
		}
		return policy, nil
	}
	msg, err := oneMessage(specific)
	if err != nil {
		return nil, err
	}
	versions, err := intList(findField(msg, "versions"), 64)
	if err != nil {
		return nil, err
	}
	return &VersionPolicy{Kind: VersionPolicySpecific, Versions: versions}, nil
}

func unsupportedFields(fields []field) ([]string, error) {
	names := []string{"sequence_batching", "ensemble_scheduling", "response_cache"}
	var set []string
	for _, name := range names {
		vals := findField(fields, name)
		if vals == nil {
			continue
		}
		if _, err := oneMessage(vals); err != nil {
			return nil, err
		}
		set = append(set, name)
	}
	return set, nil
}

func unsupportedReason(names []string) string {
	switch len(names) {
	case 1:
		return names[0] + " is not supported"
	case 2:
		return names[0] + " and " + names[1] + " are not supported"
	default:
		return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1] + " are not supported"
	}
}

func oneString(vals []value) (string, error) {
	if len(vals) != 1 || vals[0].kind != kindString {
		return "", errBadText
	}
	return vals[0].text, nil
}

func oneMessage(vals []value) ([]field, error) {
	if len(vals) != 1 || vals[0].kind != kindMessage {
		return nil, errBadText
	}
	return vals[0].msg, nil
}

func oneInt(vals []value, bitSize int) (int64, error) {
	if len(vals) != 1 || vals[0].kind != kindNumber {
		return 0, errBadText
	}
	return parseIntToken(vals[0].text, bitSize)
}

func oneUint(vals []value, bitSize int) (uint64, error) {
	if len(vals) != 1 || vals[0].kind != kindNumber {
		return 0, errBadText
	}
	return parseUintToken(vals[0].text, bitSize)
}

func intList(vals []value, bitSize int) ([]int64, error) {
	var out []int64
	for _, item := range vals {
		switch item.kind {
		case kindNumber:
			n, err := parseIntToken(item.text, bitSize)
			if err != nil {
				return nil, err
			}
			out = append(out, n)
		case kindList:
			for _, nested := range item.list {
				if nested.kind != kindNumber {
					return nil, errBadText
				}
				n, err := parseIntToken(nested.text, bitSize)
				if err != nil {
					return nil, err
				}
				out = append(out, n)
			}
		default:
			return nil, errBadText
		}
	}
	return out, nil
}

func newString(v string) *string { return &v }

func newInt(v int) *int { return &v }

func newUint64(v uint64) *uint64 { return &v }
