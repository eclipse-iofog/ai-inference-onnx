package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Pins are the runtime inputs that are not Go modules.
// Versions come from scripts/build-onnxruntime.sh so the SBOM cannot drift from the build.
type Pins struct {
	ORTVersion string
	ORTCommit  string
	ORTRepo    string

	CUDA12          string
	CUDNN12         string
	CUDA13          string
	CUDNN13         string
	TRT12           string
	TRT12CUDA       string
	TRT12CUDNN      string
	TRT13AMD64      string
	TRT13AMD64CUDA  string
	TRT13AMD64CUDNN string
	TRT13ARM64      string
	TRT13ARM64CUDA  string
	TRT13ARM64CUDNN string
	OpenVINO        string
	QNN             string
	ROCm            string
	Go              string
}

var (
	assignRE = regexp.MustCompile(`(?m)^([A-Z0-9_]+)=([^\s#]+)\s*$`)
	commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
	goLineRE = regexp.MustCompile(`(?m)^go (1\.\d+\.\d+)\s*$`)
)

func parsePins(path, rootGoMod, dashboardGoMod string) (Pins, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Pins{}, err
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	vals := map[string]string{}
	for _, m := range assignRE.FindAllStringSubmatch(text, -1) {
		vals[m[1]] = m[2]
	}
	goVer, err := matchingGoVersion(rootGoMod, dashboardGoMod)
	if err != nil {
		return Pins{}, err
	}
	pins := Pins{
		ORTVersion:      vals["ORT_VERSION"],
		ORTCommit:       vals["ORT_COMMIT"],
		ORTRepo:         vals["ORT_REPO"],
		CUDA12:          vals["CUDA12_VERSION"],
		CUDNN12:         vals["CUDNN12_VERSION"],
		CUDA13:          vals["CUDA13_VERSION"],
		CUDNN13:         vals["CUDNN13_VERSION"],
		TRT12:           vals["TENSORRT12_VERSION"],
		TRT12CUDA:       vals["TENSORRT12_CUDA_VERSION"],
		TRT12CUDNN:      vals["TENSORRT12_CUDNN_VERSION"],
		TRT13AMD64:      vals["TENSORRT13_AMD64_VERSION"],
		TRT13AMD64CUDA:  vals["TENSORRT13_AMD64_CUDA_VERSION"],
		TRT13AMD64CUDNN: vals["TENSORRT13_AMD64_CUDNN_VERSION"],
		TRT13ARM64:      vals["TENSORRT13_ARM64_VERSION"],
		TRT13ARM64CUDA:  vals["TENSORRT13_ARM64_CUDA_VERSION"],
		TRT13ARM64CUDNN: vals["TENSORRT13_ARM64_CUDNN_VERSION"],
		OpenVINO:        vals["OPENVINO_VERSION"],
		QNN:             vals["QNN_VERSION"],
		ROCm:            vals["ROCM_VERSION"],
		Go:              goVer,
	}
	if err := pins.validate(); err != nil {
		return Pins{}, fmt.Errorf("%s: %w", path, err)
	}
	return pins, nil
}

func matchingGoVersion(rootMod, dashboardMod string) (string, error) {
	root, err := readGoVersion(rootMod)
	if err != nil {
		return "", err
	}
	dash, err := readGoVersion(dashboardMod)
	if err != nil {
		return "", err
	}
	if root != dash {
		return "", fmt.Errorf("go version %s in %s and %s in %s", root, rootMod, dash, dashboardMod)
	}
	return root, nil
}

func readGoVersion(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	m := goLineRE.FindStringSubmatch(string(raw))
	if m == nil {
		return "", fmt.Errorf("%s has no go version", path)
	}
	return m[1], nil
}

func (p Pins) validate() error {
	required := []string{
		p.ORTVersion, p.ORTCommit, p.ORTRepo, p.Go,
		p.CUDA12, p.CUDNN12, p.CUDA13, p.CUDNN13,
		p.TRT12, p.TRT12CUDA, p.TRT12CUDNN,
		p.TRT13AMD64, p.TRT13AMD64CUDA, p.TRT13AMD64CUDNN,
		p.TRT13ARM64, p.TRT13ARM64CUDA, p.TRT13ARM64CUDNN,
		p.OpenVINO, p.QNN, p.ROCm,
	}
	for _, v := range required {
		if v == "" {
			return fmt.Errorf("missing a runtime pin or the Go version")
		}
	}
	if !commitRE.MatchString(p.ORTCommit) {
		return fmt.Errorf("ORT_COMMIT must be 40 hex digits, got %q", p.ORTCommit)
	}
	if !strings.HasPrefix(p.ORTRepo, "https://") {
		return fmt.Errorf("ORT_REPO must be an https URL, got %q", p.ORTRepo)
	}
	return nil
}
