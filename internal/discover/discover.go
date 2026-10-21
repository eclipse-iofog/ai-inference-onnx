// Package discover resolves model directories under a models root.
// It selects ONNX files and versions, and reads which knowledge directories a model names.
// It does not load a session.
package discover

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
)

const (
	modelFileName = "model.onnx"

	reasonNoONNX       = "no ONNX model in directory"
	reasonSeveralONNX  = "several ONNX files and no model.onnx"
	reasonNumVersions  = "num_versions must be at least 1"
	reasonNoneSelected = "version policy selected no version"
	reasonUnreadable   = "model directory could not be read"
	reasonConfig       = "config.pbtxt could not be read"
)

// Model is one child directory of a models root.
type Model struct {
	// Name is the directory name.
	Name string
	// Available is false when the directory cannot be served.
	Available bool
	// Reason explains why the directory is unavailable. It is empty when Available is true.
	Reason string
	// Versions are the ONNX files the version policy kept, highest version first.
	Versions []Version
	// MaxBatchSize is the batch cap from the model file, including 0.
	// It is nil when the file omits it. A batchable model is not assigned a batch size here.
	MaxBatchSize *int
	// MaxQueueDelayMicroseconds is the queue delay from the model file, including 0.
	// It is nil when the file omits it.
	MaxQueueDelayMicroseconds *uint64
}

// Version is one selected ONNX file.
type Version struct {
	// Version is the decimal version. A directory with no version subdirectories is "1".
	Version string
	// Path is the ONNX file.
	Path string
	// DataPath is the external-data sibling of Path. The file may be absent.
	DataPath string
	// DataPresent reports whether DataPath is a regular file.
	DataPresent bool
}

// Selected reports the artifact a version string would serve.
// An empty version is the highest selected version.
// A name that is not a selected decimal version is not selected.
// That includes "latest", "v1", a number with a leading zero, and a version the policy left out.
func (m Model) Selected(version string) (Version, bool) {
	if !m.Available || len(m.Versions) == 0 {
		return Version{}, false
	}
	if version == "" {
		best := m.Versions[0]
		bestN, ok := versionNumber(best.Version)
		if !ok {
			return Version{}, false
		}
		for _, item := range m.Versions[1:] {
			n, ok := versionNumber(item.Version)
			if ok && n > bestN {
				best = item
				bestN = n
			}
		}
		return best, true
	}
	if _, ok := versionNumber(version); !ok {
		return Version{}, false
	}
	for _, item := range m.Versions {
		if item.Version == version {
			return item, true
		}
	}
	return Version{}, false
}

// Resolve lists each child directory of a models root.
// A symlink to a directory is a model, and the link name is the model name.
// A name that starts with ".." is not a model. A regular file in the root is ignored.
// The knowledge root is not an argument and is not read.
// Each directory is either selected ONNX files or unavailable with a reason.
func Resolve(root string) ([]Model, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read models root: %w", err)
	}
	var models []Model
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "..") {
			continue
		}
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || !info.IsDir() {
			continue
		}
		models = append(models, resolveChild(root, name))
	}
	slices.SortFunc(models, func(a, b Model) int {
		return strings.Compare(a.Name, b.Name)
	})
	return models, nil
}

func resolveChild(root, name string) Model {
	model := Model{Name: name}
	if strings.ContainsAny(name, "*>") {
		model.Reason = fmt.Sprintf("name %q is rejected", name)
		return model
	}
	dir := filepath.Join(root, name)
	parsed, reason := loadConfig(dir, name)
	if reason != "" {
		model.Reason = reason
		return model
	}
	versions, reason := collectVersions(dir, parsed.DefaultModelFilename)
	if reason != "" {
		model.Reason = reason
		return model
	}
	selected, reason := applyPolicy(versions, parsed.VersionPolicy)
	if reason != "" {
		model.Reason = reason
		return model
	}
	model.Available = true
	model.Versions = selected
	model.MaxBatchSize = parsed.MaxBatchSize
	model.MaxQueueDelayMicroseconds = parsed.MaxQueueDelayMicroseconds
	return model
}

func loadConfig(dir, name string) (config.ModelFile, string) {
	data, err := os.ReadFile(filepath.Join(dir, "config.pbtxt")) // #nosec G304 -- config file is config.pbtxt in the model directory, not a request path
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config.ParseModelFile(name, nil), ""
		}
		return config.ModelFile{}, reasonConfig
	}
	parsed := config.ParseModelFile(name, data)
	if !parsed.Available {
		return parsed, parsed.Reason
	}
	return parsed, ""
}

func collectVersions(dir string, defaultName *string) ([]Version, string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, reasonUnreadable
	}
	var numeric []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, ok := versionNumber(entry.Name()); !ok {
			continue
		}
		numeric = append(numeric, entry.Name())
	}
	if len(numeric) > 0 {
		versions, reason, versioned := resolveVersionDirs(dir, numeric, defaultName)
		if versioned {
			return versions, reason
		}
	}
	version, status := resolveONNX(dir, defaultName, false)
	switch status {
	case onnxFound:
		version.Version = "1"
		return []Version{version}, ""
	case onnxAmbiguous:
		return nil, reasonSeveralONNX
	case onnxMissingName:
		return nil, missingNameReason(defaultName)
	case onnxUnreadable:
		return nil, reasonUnreadable
	default:
		return nil, reasonNoONNX
	}
}

func resolveVersionDirs(dir string, numeric []string, defaultName *string) ([]Version, string, bool) {
	var found []Version
	var failure string
	contained := false
	for _, name := range numeric {
		version, status := resolveONNX(filepath.Join(dir, name), defaultName, true)
		switch status {
		case onnxNone:
			continue
		case onnxUnreadable:
			return nil, reasonUnreadable, true
		case onnxAmbiguous:
			contained = true
			if failure == "" {
				failure = reasonSeveralONNX
			}
		case onnxMissingName:
			contained = true
			if failure == "" {
				failure = missingNameReason(defaultName)
			}
		default:
			contained = true
			version.Version = name
			found = append(found, version)
		}
	}
	if !contained {
		return nil, "", false
	}
	if failure != "" {
		return nil, failure, true
	}
	return found, "", true
}

type onnxStatus int

const (
	onnxFound onnxStatus = iota
	onnxNone
	onnxAmbiguous
	onnxMissingName
	onnxUnreadable
)

// resolveONNX picks the ONNX file at the top of dir.
// model.onnx wins when that file exists.
// Otherwise exactly one top-level .onnx file is used.
// Otherwise defaultName names the file.
// A directory named model.onnx is accepted only when allowModelDir is set,
// and only when that directory contains a file named model.onnx.
// Nested files do not count.
func resolveONNX(dir string, defaultName *string, allowModelDir bool) (Version, onnxStatus) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Version{}, onnxUnreadable
	}
	var others []string
	modelFile := false
	modelDir := false
	for _, entry := range entries {
		name := entry.Name()
		if name == modelFileName {
			if entry.IsDir() {
				modelDir = true
			} else {
				modelFile = true
			}
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".onnx") {
			continue
		}
		others = append(others, name)
	}
	if modelFile {
		return versionFile(filepath.Join(dir, modelFileName)), onnxFound
	}
	if allowModelDir && modelDir {
		inner := filepath.Join(dir, modelFileName, modelFileName)
		info, err := os.Stat(inner)
		if err == nil && info.Mode().IsRegular() {
			return versionFile(inner), onnxFound
		}
	}
	if len(others) == 1 {
		return versionFile(filepath.Join(dir, others[0])), onnxFound
	}
	if name, ok := usableFilename(defaultName); ok {
		if version, ok := versionForName(dir, name, allowModelDir); ok {
			return version, onnxFound
		}
		if len(others) > 1 {
			return Version{}, onnxMissingName
		}
		return Version{}, onnxNone
	}
	if len(others) > 1 {
		return Version{}, onnxAmbiguous
	}
	return Version{}, onnxNone
}

func versionForName(dir, name string, allowModelDir bool) (Version, bool) {
	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil {
		return Version{}, false
	}
	if info.Mode().IsRegular() {
		return versionFile(path), true
	}
	if allowModelDir && info.IsDir() {
		inner := filepath.Join(path, modelFileName)
		innerInfo, err := os.Stat(inner)
		if err == nil && innerInfo.Mode().IsRegular() {
			return versionFile(inner), true
		}
	}
	return Version{}, false
}

func versionFile(path string) Version {
	dataPath := path + ".data"
	present := false
	info, err := os.Stat(dataPath)
	if err == nil && info.Mode().IsRegular() {
		present = true
	}
	return Version{Path: path, DataPath: dataPath, DataPresent: present}
}

func usableFilename(name *string) (string, bool) {
	if name == nil {
		return "", false
	}
	text := *name
	if text == "" || filepath.IsAbs(text) || strings.ContainsAny(text, `/\`) {
		return "", false
	}
	cleaned := filepath.Clean(text)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "..") {
		return "", false
	}
	return cleaned, true
}

func missingNameReason(name *string) string {
	if name == nil {
		return reasonSeveralONNX
	}
	return fmt.Sprintf("default model file %q was not found", *name)
}

func applyPolicy(versions []Version, policy *config.VersionPolicy) ([]Version, string) {
	if len(versions) == 0 {
		return nil, reasonNoONNX
	}
	ordered := copyVersions(versions)
	slices.SortFunc(ordered, func(a, b Version) int {
		an, _ := versionNumber(a.Version)
		bn, _ := versionNumber(b.Version)
		return cmp.Compare(bn, an)
	})
	if policy == nil {
		return copyVersions(ordered[:1]), ""
	}
	switch policy.Kind {
	case config.VersionPolicyAll:
		return ordered, ""
	case config.VersionPolicyLatest:
		if policy.NumVersions < 1 {
			return nil, reasonNumVersions
		}
		n := 0
		var seen uint32
		for n < len(ordered) && seen < policy.NumVersions {
			n++
			seen++
		}
		return copyVersions(ordered[:n]), ""
	case config.VersionPolicySpecific:
		want := make(map[int64]struct{}, len(policy.Versions))
		for _, version := range policy.Versions {
			want[version] = struct{}{}
		}
		var kept []Version
		for _, version := range ordered {
			n, ok := versionNumber(version.Version)
			if !ok {
				continue
			}
			if _, ok := want[n]; ok {
				kept = append(kept, version)
			}
		}
		if len(kept) == 0 {
			return nil, reasonNoneSelected
		}
		return kept, ""
	default:
		return nil, reasonNoneSelected
	}
}

func copyVersions(versions []Version) []Version {
	out := make([]Version, len(versions))
	copy(out, versions)
	return out
}

// versionNumber accepts digits only, with no leading zero.
// "10" is greater than "9". "0", "01", "v1", and "latest" are not versions.
func versionNumber(name string) (int64, bool) {
	if name == "" || name[0] == '0' {
		return 0, false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(name, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
