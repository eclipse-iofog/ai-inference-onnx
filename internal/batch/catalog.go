package batch

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const (
	// StateReady means the model directory can serve.
	StateReady = "READY"
	// StateLoading means the model directory is being loaded.
	StateLoading = "LOADING"
	// StateUnavailable means the model directory cannot serve.
	StateUnavailable = "UNAVAILABLE"
)

// Directory is one model directory in a repository snapshot.
type Directory struct {
	// Name is the directory name.
	Name string
	// State is ready, loading, or unavailable.
	State string
	// Reason is empty when State is ready.
	Reason string
	// Versions are the selected versions. The first is the one served when a caller omits the version.
	// An empty list means the directory has no selected version.
	Versions []ModelVersion
}

// ModelVersion is one selected version and, when a session is open, its signature.
type ModelVersion struct {
	// Version is the decimal version.
	Version string
	// Signature is the model signature. It is empty when no session is open.
	Signature Signature
}

// IndexEntry is one row of the repository index.
type IndexEntry struct {
	// Name is the directory name.
	Name string `json:"name"`
	// Version is the selected version. It is omitted when the directory has no selected version.
	Version string `json:"version,omitempty"`
	// State is ready, loading, or unavailable.
	State string `json:"state"`
	// Reason is empty when State is ready.
	Reason string `json:"reason"`
}

// Names lists directories that have a selected version, in Go string order.
// A directory with no selected version is omitted. The result is never nil.
func Names(directories []Directory) []string {
	names := make([]string, 0)
	seen := make(map[string]struct{}, len(directories))
	for _, dir := range directories {
		if len(dir.Versions) == 0 {
			continue
		}
		if _, ok := seen[dir.Name]; ok {
			continue
		}
		seen[dir.Name] = struct{}{}
		names = append(names, dir.Name)
	}
	slices.Sort(names)
	return names
}

// VersionNames returns the selected version strings in numeric order.
// The result is never nil.
func VersionNames(versions []ModelVersion) []string {
	names := make([]string, 0, len(versions))
	for _, version := range versions {
		if version.Version == "" {
			continue
		}
		names = append(names, version.Version)
	}
	slices.SortFunc(names, compareVersions)
	return names
}

// Index lists every directory. readyOnly keeps rows that can serve.
// A directory with no selected version is one row and omits the version.
// One selected version is one row. Rows are ordered by name, then by version number.
// Reason is empty when the state is ready. The result is never nil.
func Index(directories []Directory, readyOnly bool) []IndexEntry {
	rows := make([]IndexEntry, 0)
	for _, dir := range directories {
		if readyOnly && dir.State != StateReady {
			continue
		}
		reason := dir.Reason
		if dir.State == StateReady {
			reason = ""
		}
		if len(dir.Versions) == 0 {
			rows = append(rows, IndexEntry{Name: dir.Name, State: dir.State, Reason: reason})
			continue
		}
		for _, version := range dir.Versions {
			rows = append(rows, IndexEntry{
				Name:    dir.Name,
				Version: version.Version,
				State:   dir.State,
				Reason:  reason,
			})
		}
	}
	slices.SortFunc(rows, func(a, b IndexEntry) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return compareVersions(a.Version, b.Version)
	})
	return rows
}

// Select returns the directory and version addressed by name and version.
// An empty version is the first selected version, which is the one the server serves.
// A version that was not selected is not found, including while the directory is loading.
// A loading directory returns the loading result. An unavailable directory returns its stored reason.
func Select(directories []Directory, name, version string) (Directory, ModelVersion, error) {
	var found *Directory
	for i := range directories {
		if directories[i].Name == name {
			found = &directories[i]
			break
		}
	}
	if found == nil {
		return Directory{}, ModelVersion{}, NotFound(fmt.Sprintf("model %q was not found", name))
	}
	chosen, ok := chooseVersion(*found, version)
	if !ok {
		return Directory{}, ModelVersion{}, NotFound(fmt.Sprintf("version %q was not found", version))
	}
	switch found.State {
	case StateLoading:
		return *found, chosen, Loading(found.Reason)
	case StateUnavailable:
		return *found, chosen, Unavailable(found.Reason)
	case StateReady:
		return *found, chosen, nil
	default:
		if found.Reason != "" {
			return *found, chosen, Unavailable(found.Reason)
		}
		return Directory{}, ModelVersion{}, NotFound(fmt.Sprintf("model %q was not found", name))
	}
}

func chooseVersion(dir Directory, version string) (ModelVersion, bool) {
	if version != "" {
		for _, item := range dir.Versions {
			if item.Version == version {
				return item, true
			}
		}
		return ModelVersion{}, false
	}
	if len(dir.Versions) == 0 {
		return ModelVersion{}, true
	}
	return dir.Versions[0], true
}

func compareVersions(a, b string) int {
	an, aerr := strconv.ParseInt(a, 10, 64)
	bn, berr := strconv.ParseInt(b, 10, 64)
	if aerr == nil && berr == nil && an != bn {
		return cmp.Compare(an, bn)
	}
	return strings.Compare(a, b)
}
