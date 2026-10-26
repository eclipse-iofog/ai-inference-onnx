package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := flag.String("root", ".", "repository root")
	check := flag.Bool("check", false, "fail if committed root documents differ")
	release := flag.Bool("release", false, "write a per-artifact SBOM")
	subjectKind := flag.String("subject", "", "engine or runtime")
	variant := flag.String("variant", "", "runtime variant")
	arch := flag.String("arch", "", "amd64, arm64, or riscv64")
	version := flag.String("version", "", "release version stamped into a per-artifact SBOM")
	stage := flag.String("stage", "", "exported image root or unpacked runtime archive")
	outDir := flag.String("out", "", "directory for per-artifact SBOM files")
	flag.Parse()

	if *check && *release {
		fmt.Fprintln(os.Stderr, "-check and -release are different documents")
		os.Exit(2)
	}
	repo, err := filepath.Abs(*root)
	if err != nil {
		fail(err)
	}
	// go mod download records zip sums for every module in the build list,
	// including modules the engine and the dashboard do not import. Those sums
	// are not part of the SBOM output, and committing them makes the vulnerability
	// scanner report the modules.
	sums := snapshotSums(repo)
	comps, subject, err := buildInventory(repo)
	if rerr := restoreSums(repo, sums); rerr != nil && err == nil {
		err = rerr
	}
	if err != nil {
		fail(err)
	}
	if *release {
		if err := writeRelease(repo, comps, *subjectKind, *variant, *arch, *version, *stage, *outDir); err != nil {
			fail(err)
		}
		return
	}
	files, err := rootDocuments(subject, comps)
	if err != nil {
		fail(err)
	}
	if *check {
		if err := checkFiles(repo, files); err != nil {
			fail(err)
		}
		fmt.Println("sbom check passed")
		return
	}
	if err := writeFiles(repo, files); err != nil {
		fail(err)
	}
	fmt.Println("wrote SBOM.md, LICENSE-3RD-PARTY.md, sbom.cdx.json, sbom.spdx.json")
}

func rootDocuments(subject Component, comps []Component) (map[string][]byte, error) {
	// The vulnerability scanner matches every package URL in the JSON files and does not read CycloneDX scope.
	// SBOM.md and the license notices keep the full inventory.
	published := publishedComponents(comps)
	cdx, err := renderCDX(subject, published)
	if err != nil {
		return nil, err
	}
	spdx, err := renderSPDX(subject, published)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		"SBOM.md":              renderSBOM(subject, comps),
		"LICENSE-3RD-PARTY.md": renderNotices(comps),
		"sbom.cdx.json":        cdx,
		"sbom.spdx.json":       spdx,
	}, nil
}

// publishedComponents is the inventory written to sbom.cdx.json and sbom.spdx.json.
// Excluded modules are not compiled into a shipped binary. A component with no
// version of its own matches upstream advisories by package name.
func publishedComponents(comps []Component) []Component {
	out := make([]Component, 0, len(comps))
	for _, c := range comps {
		if c.Scope == scopeExcluded || c.Version == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

func snapshotSums(repo string) map[string][]byte {
	out := map[string][]byte{}
	for _, rel := range []string{"go.sum", filepath.Join("dashboard", "go.sum")} {
		body, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			continue
		}
		out[rel] = body
	}
	return out
}

func restoreSums(repo string, before map[string][]byte) error {
	var err error
	for rel, body := range before {
		if werr := os.WriteFile(filepath.Join(repo, rel), body, 0o644); werr != nil && err == nil {
			err = werr
		}
	}
	return err
}

func writeRelease(repo string, comps []Component, kind, variant, arch, version, stage, outDir string) error {
	if kind != subjectEngine && kind != subjectRuntime {
		return fmt.Errorf("-subject must be engine or runtime")
	}
	if variant == "" || arch == "" || version == "" {
		return fmt.Errorf("-variant, -arch, and -version are required")
	}
	if stage == "" || outDir == "" {
		return fmt.Errorf("-stage and -out are required")
	}
	pins, err := parsePins(
		filepath.Join(repo, "scripts/build-onnxruntime.sh"),
		filepath.Join(repo, "go.mod"),
		filepath.Join(repo, "dashboard/go.mod"),
	)
	if err != nil {
		return err
	}
	if kind == subjectRuntime && version != pins.ORTVersion {
		return fmt.Errorf("-version for a runtime SBOM must be %s", pins.ORTVersion)
	}
	subject, selected, err := selectRelease(comps, kind, variant, arch, version)
	if err != nil {
		return err
	}
	subject, selected, err = applyStage(subject, selected, stage, kind, pins)
	if err != nil {
		return err
	}
	cdx, err := renderCDX(subject, selected)
	if err != nil {
		return err
	}
	spdx, err := renderSPDX(subject, selected)
	if err != nil {
		return err
	}
	base, err := releaseBase(kind, version, variant, arch, pins.ORTVersion)
	if err != nil {
		return err
	}
	files := map[string][]byte{
		base + ".cdx.json":  cdx,
		base + ".spdx.json": spdx,
	}
	if err := writeFiles(outDir, files); err != nil {
		return err
	}
	fmt.Printf("wrote %s.cdx.json and %s.spdx.json\n", base, base)
	return nil
}

func writeFiles(dir string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func checkFiles(dir string, files map[string][]byte) error {
	var mismatched []string
	for _, name := range []string{"SBOM.md", "LICENSE-3RD-PARTY.md", "sbom.cdx.json", "sbom.spdx.json"} {
		have, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		want := files[name]
		if !bytes.Equal(have, want) {
			mismatched = append(mismatched, fmt.Sprintf("%s\n%s", name, firstDiff(have, want)))
		}
	}
	if len(mismatched) == 0 {
		return nil
	}
	return fmt.Errorf("generated SBOM documents are stale; run make sbom\n%s", strings.Join(mismatched, "\n"))
}

func firstDiff(have, want []byte) string {
	h := bytes.Split(have, []byte("\n"))
	w := bytes.Split(want, []byte("\n"))
	n := len(h)
	if len(w) < n {
		n = len(w)
	}
	for i := 0; i < n; i++ {
		if !bytes.Equal(h[i], w[i]) {
			return fmt.Sprintf("line %d\n committed: %s\n generated: %s", i+1, trunc(h[i]), trunc(w[i]))
		}
	}
	return fmt.Sprintf("length committed %d generated %d", len(have), len(want))
}

func trunc(b []byte) string {
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "licensegen: %v\n", err)
	os.Exit(1)
}
