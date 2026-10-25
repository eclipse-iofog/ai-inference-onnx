package main

import "strings"

const (
	scopeRequired = "required"
	scopeOptional = "optional"
	scopeExcluded = "excluded"

	relDepends  = "depends"
	relContains = "contains"
	relTest     = "test"
	relExcluded = "excluded"

	subjectEngine  = "engine"
	subjectRuntime = "runtime"

	propClosures      = "inference:closures"
	propArch          = "inference:arch"
	propVariant       = "inference:variant"
	propCommit        = "inference:commit"
	propStagedPath    = "inference:staged-path"
	propStagedLibrary = "inference:staged-library"
	propStagedImage   = "inference:staged-image"
	propLibTree       = "inference:lib-tree-sha256"
	propContainedBy   = "inference:contained-by"
	propImportPath    = "inference:import-path"
	imageBoth         = "both"
	imageEngine       = "engine"
	imageRuntime      = "runtime"
)

// Component is one SBOM entry. Ref is the CycloneDX bom-ref and must be unique.
type Component struct {
	Ref         string
	Type        string
	Name        string
	Version     string
	PURL        string
	Scope       string
	License     string
	LicenseText string
	Closures    []string
	Hashes      []Hash
	Props       []Prop
	Note        string
	Relation    string
}

type Hash struct {
	Alg     string
	Content string
}

type Prop struct {
	Name  string
	Value string
}

func (c Component) spdxID() string {
	var b strings.Builder
	b.WriteString("SPDXRef-")
	for _, r := range c.Ref {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func prop(name, value string) Prop {
	return Prop{Name: name, Value: value}
}
