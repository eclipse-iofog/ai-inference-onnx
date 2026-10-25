package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func selectRelease(comps []Component, kind, variant, arch, version string) (Component, []Component, error) {
	closure, err := closureFor(variant, arch)
	if err != nil {
		return Component{}, nil, err
	}
	var kept []Component
	for _, c := range comps {
		if c.Scope != scopeRequired || !releaseMatch(c, kind, closure) {
			continue
		}
		kept = append(kept, c)
	}
	subject, err := releaseSubject(kind, variant, arch, version)
	if err != nil {
		return Component{}, nil, err
	}
	sortComponents(kept)
	return subject, kept, nil
}

func releaseMatch(c Component, kind, closure string) bool {
	switch kind {
	case subjectEngine:
		return hasClosure(c, "engine") || hasClosure(c, closure)
	case subjectRuntime:
		return hasClosure(c, closure)
	default:
		return false
	}
}

func hasClosure(c Component, name string) bool {
	for _, cl := range c.Closures {
		if cl == name {
			return true
		}
	}
	return false
}

func releaseSubject(kind, variant, arch, version string) (Component, error) {
	switch kind {
	case subjectEngine:
		ref := fmt.Sprintf("pkg:generic/ai-inference-onnx@%s?arch=%s&variant=%s", version, arch, variant)
		return Component{
			Ref:      ref,
			Type:     "application",
			Name:     "ai-inference-onnx",
			Version:  version,
			PURL:     ref,
			Scope:    scopeRequired,
			License:  "EPL-2.0",
			Props:    []Prop{prop(propArch, arch), prop(propVariant, variant)},
			Relation: relDepends,
			Note:     "One published engine image. The binary loads the ONNX Runtime shared library at run time.",
		}, nil
	case subjectRuntime:
		ref := fmt.Sprintf("pkg:generic/onnxruntime@%s?arch=%s&variant=%s", version, arch, variant)
		return Component{
			Ref:      ref,
			Type:     "library",
			Name:     "ONNX Runtime",
			Version:  version,
			PURL:     ref,
			Scope:    scopeRequired,
			License:  "MIT",
			Props:    []Prop{prop(propArch, arch), prop(propVariant, variant)},
			Relation: relDepends,
			Note:     "One published runtime image. Provider SDK libraries that were compiled against stay in the engine image.",
		}, nil
	default:
		return Component{}, fmt.Errorf("subject must be engine or runtime")
	}
}

func releaseBase(kind, version, variant, arch, ortVersion string) (string, error) {
	switch kind {
	case subjectEngine:
		return fmt.Sprintf("inference-%s-ort%s-%s-%s", version, ortVersion, variant, arch), nil
	case subjectRuntime:
		return fmt.Sprintf("onnxruntime-%s-%s-linux-%s", version, variant, arch), nil
	default:
		return "", fmt.Errorf("subject must be engine or runtime")
	}
}

func applyStage(subject Component, comps []Component, stage, kind string, pins Pins) (Component, []Component, error) {
	info, err := os.Stat(stage)
	if err != nil {
		return Component{}, nil, err
	}
	if !info.IsDir() {
		return Component{}, nil, fmt.Errorf("%s is not a directory", stage)
	}
	if kind == subjectRuntime {
		if err := checkRuntimeIdentity(stage, pins); err != nil {
			return Component{}, nil, err
		}
	}
	if kind == subjectEngine {
		sum, rel, err := hashBinary(stage)
		if err != nil {
			return Component{}, nil, err
		}
		subject.Hashes = []Hash{{Alg: "SHA-256", Content: sum}}
		subject.Props = append(subject.Props, prop(propStagedPath, rel))
	}
	libDirs := libraryDirs(stage)
	for i := range comps {
		needle := propValue(comps[i], propStagedLibrary)
		if needle == "" {
			continue
		}
		where := propValue(comps[i], propStagedImage)
		rel, sum, err := findLibrary(stage, libDirs, needle)
		if err != nil {
			return Component{}, nil, err
		}
		if rel == "" {
			if libraryRequired(where, kind) {
				return Component{}, nil, fmt.Errorf("stage is missing %s for %s", needle, comps[i].Name)
			}
			continue
		}
		comps[i].Hashes = []Hash{{Alg: "SHA-256", Content: sum}}
		comps[i].Props = append(comps[i].Props, prop(propStagedPath, rel))
		if needle == "libonnxruntime.so."+pins.ORTVersion {
			dir := filepath.Dir(filepath.Join(stage, filepath.FromSlash(rel)))
			tree, err := treeHash(dir)
			if err != nil {
				return Component{}, nil, err
			}
			comps[i].Props = append(comps[i].Props, prop(propLibTree, tree))
		}
	}
	licensed, err := licenseFiles(stage, subject.Ref, subject.Version)
	if err != nil {
		return Component{}, nil, err
	}
	comps = append(comps, licensed...)
	sortComponents(comps)
	return subject, comps, nil
}

func libraryRequired(where, kind string) bool {
	switch where {
	case imageBoth:
		return true
	case imageEngine:
		return kind == subjectEngine
	case imageRuntime:
		return kind == subjectRuntime
	default:
		return false
	}
}

func checkRuntimeIdentity(stage string, pins Pins) error {
	version, err := readTrimmed(filepath.Join(stage, "VERSION"))
	if err != nil {
		return fmt.Errorf("VERSION: %w", err)
	}
	if version != pins.ORTVersion {
		return fmt.Errorf("VERSION is %s, pin is %s", version, pins.ORTVersion)
	}
	commit, err := readTrimmed(filepath.Join(stage, "COMMIT"))
	if err != nil {
		return fmt.Errorf("COMMIT: %w", err)
	}
	if commit != pins.ORTCommit {
		return fmt.Errorf("COMMIT is %s, pin is %s", commit, pins.ORTCommit)
	}
	return nil
}

func hashBinary(stage string) (string, string, error) {
	for _, rel := range []string{"usr/local/bin/inference", "inference"} {
		path := filepath.Join(stage, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", "", err
		}
		if info.IsDir() {
			continue
		}
		sum, err := hashFile(path)
		if err != nil {
			return "", "", err
		}
		return sum, rel, nil
	}
	return "", "", fmt.Errorf("stage is missing the inference binary")
}

func libraryDirs(stage string) []string {
	var out []string
	for _, rel := range []string{"lib", "opt/onnxruntime/lib", "usr/lib64", "opt/rocm/lib"} {
		path := filepath.Join(stage, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			out = append(out, path)
		}
	}
	return out
}

func findLibrary(stage string, dirs []string, needle string) (string, string, error) {
	type hit struct {
		rel  string
		name string
		link bool
	}
	var hits []hit
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if !matchLibrary(d.Name(), needle) {
				return nil
			}
			rel, err := filepath.Rel(stage, path)
			if err != nil {
				return err
			}
			hits = append(hits, hit{
				rel:  filepath.ToSlash(rel),
				name: d.Name(),
				link: d.Type()&os.ModeSymlink != 0,
			})
			return nil
		})
		if err != nil {
			return "", "", err
		}
	}
	if len(hits) == 0 {
		return "", "", nil
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].link != hits[j].link {
			return !hits[i].link
		}
		if len(hits[i].name) != len(hits[j].name) {
			return len(hits[i].name) > len(hits[j].name)
		}
		return hits[i].rel < hits[j].rel
	})
	sum, err := hashFile(filepath.Join(stage, filepath.FromSlash(hits[0].rel)))
	if err != nil {
		return "", "", err
	}
	return hits[0].rel, sum, nil
}

func matchLibrary(name, needle string) bool {
	return name == needle || strings.HasPrefix(name, needle+".")
}

func licenseFiles(stage, parent, version string) ([]Component, error) {
	dir := filepath.Join(stage, "licenses")
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("licenses: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("licenses is not a directory")
	}
	var paths []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return err
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("licenses is empty")
	}
	sort.Strings(paths)
	var out []Component
	for _, rel := range paths {
		body, err := os.ReadFile(filepath.Join(stage, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		license := "NOASSERTION"
		text := ""
		if len(body) > 0 && len(body) <= 1<<20 && !strings.ContainsRune(string(body), '\x00') {
			text = normalizeNewlines(string(body))
			license = detectLicense(text)
		}
		q := url.Values{}
		q.Set("path", rel)
		ref := "pkg:generic/staged-license@" + version + "?" + q.Encode()
		out = append(out, Component{
			Ref:         ref,
			Type:        "file",
			Name:        rel,
			Version:     version,
			PURL:        ref,
			Scope:       scopeRequired,
			License:     license,
			LicenseText: text,
			Relation:    relContains,
			Hashes:      []Hash{{Alg: "SHA-256", Content: hex.EncodeToString(sum[:])}},
			Props:       []Prop{prop(propContainedBy, parent), prop(propStagedPath, rel)},
			Note:        "License file staged in the image.",
		})
	}
	return out, nil
}

func treeHash(dir string) (string, error) {
	var lines []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		sum, err := hashFile(path)
		if err != nil {
			return err
		}
		lines = append(lines, filepath.ToSlash(rel)+" "+sum)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(lines)
	return hashBytes([]byte(strings.Join(lines, "\n") + "\n")), nil
}

func propValue(c Component, name string) string {
	for _, p := range c.Props {
		if p.Name == name {
			return p.Value
		}
	}
	return ""
}

func readTrimmed(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
