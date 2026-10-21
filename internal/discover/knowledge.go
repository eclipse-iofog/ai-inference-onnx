package discover

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	knowledgeFileName = "knowledge"

	reasonKnowledgeDirectory  = "knowledge name file is a directory"
	reasonKnowledgeUnreadable = "knowledge name file could not be read"
	reasonKnowledgeEncoding   = "knowledge name file is not valid UTF-8"
)

// Knowledge is the name list at the top of one model directory.
// Names are directories under the knowledge root.
// The files inside those directories are not read.
type Knowledge struct {
	// Ready is false when the name list cannot be used or a named directory is not present.
	Ready bool
	// Reason explains why the model is not ready. It is empty when Ready is true.
	Reason string
	// Names are the unique directory names, in the order they first appear.
	// Empty means the model names no knowledge.
	Names []string
	// Missing are names that are not an existing directory.
	Missing []string
	// Fingerprint records the name file and whether each named directory exists.
	Fingerprint Fingerprint
}

// Fingerprint is the identity used to notice a knowledge change.
// It records the name path's inode, size, and modification time, and whether each named directory exists.
// It does not record names or modification times of files inside those directories.
type Fingerprint struct {
	// Present is false when the model directory has no name file.
	Present bool
	// Inode is the name path inode when the path exists.
	Inode uint64
	// Size is the name path size in bytes when the path exists.
	Size int64
	// ModTime is the name path modification time when the path exists.
	ModTime time.Time
	// Exists reports whether Names[i] is a directory, in the same order.
	// It is empty when the name file names nothing or could not be parsed.
	Exists []bool
}

// Equal reports whether the name path identity and directory presence match.
func (f Fingerprint) Equal(other Fingerprint) bool {
	if f.Present != other.Present || f.Inode != other.Inode || f.Size != other.Size || !f.ModTime.Equal(other.ModTime) {
		return false
	}
	return slices.Equal(f.Exists, other.Exists)
}

// ReadKnowledge reads the optional name file at the top of a model directory.
// A missing file, or a file that names nothing, means the model names no knowledge.
// The name file is not read from inside a version directory.
// An ONNX file inside a knowledge directory is not a model path.
func ReadKnowledge(modelDir, knowledgeRoot string) Knowledge {
	path := filepath.Join(modelDir, knowledgeFileName)
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Knowledge{Ready: true}
	}
	if err != nil {
		return Knowledge{Reason: reasonKnowledgeUnreadable}
	}
	info, err := os.Stat(path)
	if err != nil {
		return Knowledge{Reason: reasonKnowledgeUnreadable}
	}
	fingerprint := fingerprintOf(info)
	if info.IsDir() {
		return Knowledge{Reason: reasonKnowledgeDirectory, Fingerprint: fingerprint}
	}
	if !info.Mode().IsRegular() {
		return Knowledge{Reason: reasonKnowledgeUnreadable, Fingerprint: fingerprint}
	}
	data, err := os.ReadFile(path) // #nosec G304 -- name file sits beside the model config and is not taken from a request
	if err != nil {
		return Knowledge{Reason: reasonKnowledgeUnreadable, Fingerprint: fingerprint}
	}
	names, reason := parseKnowledgeNames(data)
	if reason != "" {
		return Knowledge{Reason: reason, Fingerprint: fingerprint}
	}
	if len(names) == 0 {
		return Knowledge{Ready: true, Fingerprint: fingerprint}
	}
	return knowledgePresence(names, knowledgeRoot, fingerprint)
}

func parseKnowledgeNames(data []byte) ([]string, string) {
	if !utf8.Valid(data) {
		return nil, reasonKnowledgeEncoding
	}
	seen := make(map[string]struct{})
	var names []string
	for _, line := range strings.Split(string(data), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		if !validKnowledgeName(name) {
			return nil, fmt.Sprintf("knowledge name %q is rejected", name)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names, ""
}

func validKnowledgeName(name string) bool {
	if name == "." || name == ".." {
		return false
	}
	if strings.ContainsRune(name, 0) {
		return false
	}
	return !strings.ContainsAny(name, "/\\*>")
}

func knowledgePresence(names []string, knowledgeRoot string, fingerprint Fingerprint) Knowledge {
	missing := make([]string, 0)
	parts := make([]string, 0)
	exists := make([]bool, 0, len(names))
	for _, name := range names {
		present, reason := knowledgeDir(knowledgeRoot, name)
		exists = append(exists, present)
		if present {
			continue
		}
		missing = append(missing, name)
		parts = append(parts, reason)
	}
	fingerprint.Exists = exists
	out := Knowledge{
		Names:       names,
		Missing:     missing,
		Fingerprint: fingerprint,
	}
	if len(parts) == 0 {
		out.Ready = true
		out.Missing = nil
		return out
	}
	out.Reason = strings.Join(parts, ", ")
	return out
}

func knowledgeDir(knowledgeRoot, name string) (bool, string) {
	info, err := os.Stat(filepath.Join(knowledgeRoot, name))
	if errors.Is(err, os.ErrNotExist) {
		return false, fmt.Sprintf("knowledge %q is missing", name)
	}
	if err != nil {
		return false, fmt.Sprintf("knowledge %q could not be read", name)
	}
	if !info.IsDir() {
		return false, fmt.Sprintf("knowledge %q is not a directory", name)
	}
	return true, ""
}

func fingerprintOf(info os.FileInfo) Fingerprint {
	fingerprint := Fingerprint{
		Present: true,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok {
		fingerprint.Inode = uint64(stat.Ino)
	}
	return fingerprint
}
