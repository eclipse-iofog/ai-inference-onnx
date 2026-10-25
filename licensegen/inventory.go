package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func buildInventory(repo string) ([]Component, Component, error) {
	pins, err := parsePins(
		filepath.Join(repo, "scripts/build-onnxruntime.sh"),
		filepath.Join(repo, "go.mod"),
		filepath.Join(repo, "dashboard/go.mod"),
	)
	if err != nil {
		return nil, Component{}, err
	}
	sets, err := moduleSets(repo)
	if err != nil {
		return nil, Component{}, err
	}

	goLicense, err := readNotice(repo, "go-bsd.txt")
	if err != nil {
		return nil, Component{}, err
	}

	byRef := map[string]*Component{}
	var order []string
	add := func(c Component) {
		if prev, ok := byRef[c.Ref]; ok {
			prev.Closures = uniqueStrings(append(prev.Closures, c.Closures...))
			if scopeRank(c.Scope) > scopeRank(prev.Scope) {
				prev.Scope = c.Scope
				prev.Relation = c.Relation
			}
			if prev.Note == "" {
				prev.Note = c.Note
			}
			return
		}
		cp := c
		byRef[c.Ref] = &cp
		order = append(order, c.Ref)
	}

	for _, set := range sets {
		for importPath, mod := range set.mods {
			comp, err := moduleComponent(mod)
			if err != nil {
				return nil, Component{}, err
			}
			switch {
			case len(set.required[importPath]) > 0:
				comp.Scope = scopeRequired
				comp.Relation = relDepends
				comp.Closures = uniqueStrings(set.required[importPath])
			case len(set.tested[importPath]) > 0:
				comp.Scope = scopeOptional
				comp.Relation = relTest
				comp.Closures = uniqueStrings(set.tested[importPath])
			default:
				comp.Scope = scopeExcluded
				comp.Relation = relExcluded
			}
			add(comp)
		}
	}
	runtimes, err := runtimeComponents(repo, pins)
	if err != nil {
		return nil, Component{}, err
	}
	for _, c := range runtimes {
		add(c)
	}
	add(Component{
		Ref:         "pkg:generic/go@" + pins.Go,
		Type:        "library",
		Name:        "Go",
		Version:     pins.Go,
		PURL:        "pkg:generic/go@" + pins.Go,
		Scope:       scopeRequired,
		License:     "BSD-3-Clause",
		LicenseText: goLicense,
		Closures:    []string{"dashboard", "engine"},
		Relation:    relDepends,
		Note:        "Toolchain pin from go.mod. The SBOM does not record the patch level of the machine that generated it.",
	})

	comps := make([]Component, 0, len(order))
	for _, ref := range order {
		comps = append(comps, *byRef[ref])
	}
	sortComponents(comps)
	subject := Component{
		Ref:     "pkg:golang/" + engineModule,
		Type:    "application",
		Name:    "ai-inference-onnx",
		Version: "NOASSERTION",
		PURL:    "pkg:golang/" + engineModule,
		Scope:   scopeRequired,
		License: "EPL-2.0",
		Note:    "Source tree. This document describes the engine module, the dashboard module, and the runtime pins. It does not describe one image.",
	}
	return comps, subject, nil
}

type moduleSet struct {
	mods     map[string]resolvedMod
	required map[string][]string
	tested   map[string][]string
}

func moduleSets(repo string) ([]moduleSet, error) {
	var out []moduleSet
	for _, rel := range []string{"", "dashboard"} {
		dir := repo
		if rel != "" {
			dir = filepath.Join(repo, rel)
		}
		if err := ensureModules(dir); err != nil {
			return nil, err
		}
		mods, err := listModules(dir)
		if err != nil {
			return nil, err
		}
		required := map[string][]string{}
		for _, spec := range productClosures() {
			if spec.Dir != rel {
				continue
			}
			paths, err := modulePaths(dir, spec)
			if err != nil {
				return nil, err
			}
			for path := range paths {
				if mainModule(path) {
					continue
				}
				required[path] = append(required[path], spec.Name)
			}
		}
		tested := map[string][]string{}
		for _, spec := range testClosures() {
			if spec.Dir != rel {
				continue
			}
			paths, err := modulePaths(dir, spec)
			if err != nil {
				return nil, err
			}
			for path := range paths {
				if mainModule(path) || len(required[path]) > 0 {
					continue
				}
				tested[path] = append(tested[path], spec.Name)
			}
		}
		out = append(out, moduleSet{mods: mods, required: required, tested: tested})
	}
	return out, nil
}

func scopeRank(scope string) int {
	switch scope {
	case scopeRequired:
		return 2
	case scopeOptional:
		return 1
	default:
		return 0
	}
}

func moduleComponent(mod resolvedMod) (Component, error) {
	text, err := readLicenseText(mod.Dir)
	if err != nil {
		return Component{}, fmt.Errorf("license files in %s: %w", mod.Path, err)
	}
	license := "NOASSERTION"
	if strings.TrimSpace(text) != "" {
		license = detectLicense(text)
	}
	ref := golangPURL(mod.Path, mod.Version)
	c := Component{
		Ref:         ref,
		Type:        "library",
		Name:        mod.Path,
		Version:     mod.Version,
		PURL:        ref,
		License:     license,
		LicenseText: text,
	}
	if mod.ImportPath != mod.Path {
		c.Props = append(c.Props, prop(propImportPath, mod.ImportPath))
	}
	if mod.Path == ortGoModule {
		c.Note = "Go binding. The shared library is the ONNX Runtime component, loaded at run time."
	}
	return c, nil
}

func readNotice(repo, name string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(repo, "licensegen/notice", name))
	if err != nil {
		return "", err
	}
	return normalizeNewlines(string(raw)), nil
}

func sortComponents(comps []Component) {
	sort.Slice(comps, func(i, j int) bool {
		return comps[i].Ref < comps[j].Ref
	})
	for i := range comps {
		comps[i].Closures = uniqueStrings(comps[i].Closures)
		sort.Slice(comps[i].Props, func(a, b int) bool {
			if comps[i].Props[a].Name == comps[i].Props[b].Name {
				return comps[i].Props[a].Value < comps[i].Props[b].Value
			}
			return comps[i].Props[a].Name < comps[i].Props[b].Name
		})
	}
}
