package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePins(t *testing.T) {
	pins, err := parsePins("../scripts/build-onnxruntime.sh", "../go.mod", "../dashboard/go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if pins.ORTVersion != "1.31.0" || pins.Go != "1.26.10" {
		t.Fatalf("version pins: ort %s go %s", pins.ORTVersion, pins.Go)
	}
	if pins.ORTCommit != "402979ac1bef89dcd55add41eaa7277c407599eb" {
		t.Fatalf("commit: %s", pins.ORTCommit)
	}
	if pins.CUDA12 != "12.8.1" || pins.TRT13ARM64 != "10.15.1.29" || pins.QNN != "2.51.0.260929" {
		t.Fatalf("sdk pins: %+v", pins)
	}
	if pins.TRT13AMD64CUDA == pins.TRT13ARM64CUDA {
		t.Fatal("tensorrt13 CUDA pin is the same on amd64 and arm64")
	}
}

func TestProviderLicenses(t *testing.T) {
	pins, err := parsePins("../scripts/build-onnxruntime.sh", "../go.mod", "../dashboard/go.mod")
	if err != nil {
		t.Fatal(err)
	}
	comps, err := runtimeComponents("..", pins)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"pkg:generic/cuda@" + pins.CUDA12:         "LicenseRef-NVIDIA-CUDA",
		"pkg:generic/cudnn@" + pins.CUDNN12:       "LicenseRef-NVIDIA-cuDNN",
		"pkg:generic/tensorrt@" + pins.TRT12:      "LicenseRef-NVIDIA-TensorRT",
		"pkg:generic/tensorrt@" + pins.TRT13ARM64: "LicenseRef-NVIDIA-TensorRT",
		"pkg:generic/migraphx@" + pins.ROCm:       "MIT",
		"pkg:generic/qnn@" + pins.QNN:             "LicenseRef-qcom-ai-stack",
		"pkg:generic/openvino@" + pins.OpenVINO:   "Apache-2.0",
	}
	for _, c := range comps {
		id, ok := want[c.Ref]
		if !ok {
			continue
		}
		if c.License != id {
			t.Fatalf("%s license %s", c.Ref, c.License)
		}
		if id != "NOASSERTION" && id != "Apache-2.0" && c.LicenseText == "" {
			t.Fatalf("%s has no license text", c.Ref)
		}
		if id == "NOASSERTION" && c.LicenseText != "" {
			t.Fatalf("%s has license text", c.Ref)
		}
		delete(want, c.Ref)
	}
	if len(want) != 0 {
		t.Fatalf("missing components: %v", want)
	}
}

func TestClosureFor(t *testing.T) {
	cpu, err := closureFor("cpu", "riscv64")
	if err != nil || cpu != "runtime-cpu" {
		t.Fatalf("cpu: %s %v", cpu, err)
	}
	arm, err := closureFor("tensorrt13", "arm64")
	if err != nil || arm != "runtime-tensorrt13-arm64" {
		t.Fatalf("tensorrt13 arm64: %s %v", arm, err)
	}
	if _, err := closureFor("qnn", "amd64"); err == nil {
		t.Fatal("qnn amd64 was accepted")
	}
	if _, err := closureFor("tensorrt12", "arm64"); err == nil {
		t.Fatal("tensorrt12 arm64 was accepted")
	}
}

func TestReleaseStage(t *testing.T) {
	pins, err := parsePins("../scripts/build-onnxruntime.sh", "../go.mod", "../dashboard/go.mod")
	if err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	lib := filepath.Join(stage, "lib")
	lic := filepath.Join(stage, "licenses")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(lic, 0o755); err != nil {
		t.Fatal(err)
	}
	so := filepath.Join(lib, "libonnxruntime.so."+pins.ORTVersion)
	if err := os.WriteFile(so, []byte("runtime-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lic, "LICENSE"), []byte("Permission is hereby granted, free of charge, to any person\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "VERSION"), []byte(pins.ORTVersion+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "COMMIT"), []byte(pins.ORTCommit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	comps, err := runtimeComponents("..", pins)
	if err != nil {
		t.Fatal(err)
	}
	subject, selected, err := selectRelease(comps, subjectRuntime, "cpu", "amd64", pins.ORTVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].Name != "ONNX Runtime" {
		t.Fatalf("cpu runtime components: %+v", names(selected))
	}
	subject, selected, err = applyStage(subject, selected, stage, subjectRuntime, pins)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected[0].Hashes) != 1 {
		t.Fatal("runtime library hash missing")
	}
	foundLicense := false
	for _, c := range selected {
		if c.Name == "licenses/LICENSE" {
			foundLicense = true
			if c.License != "MIT" {
				t.Fatalf("staged license: %s", c.License)
			}
		}
	}
	if !foundLicense {
		t.Fatal("staged license file missing")
	}
	cuda, selected, err := selectRelease(comps, subjectEngine, "cuda12", "amd64", "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(selected, "CUDA") || !hasName(selected, "cuDNN") || hasName(selected, "TensorRT") {
		t.Fatalf("cuda12 components: %s", names(selected))
	}
	engineStage := t.TempDir()
	for _, dir := range []string{"usr/local/bin", "opt/onnxruntime/lib", "usr/lib64", "licenses"} {
		if err := os.MkdirAll(filepath.Join(engineStage, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(engineStage, "usr/local/bin/inference"), []byte("engine"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engineStage, "opt/onnxruntime/lib/libonnxruntime.so."+pins.ORTVersion), []byte("so"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engineStage, "usr/lib64/libcudart.so.12"), []byte("cuda"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engineStage, "usr/lib64/libcudnn.so.9"), []byte("cudnn"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engineStage, "licenses/LICENSE"), []byte("Permission is hereby granted, free of charge\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cuda, selected, err = applyStage(cuda, selected, engineStage, subjectEngine, pins)
	if err != nil {
		t.Fatal(err)
	}
	if len(cuda.Hashes) != 1 {
		t.Fatal("engine binary hash missing")
	}
	_ = subject
}

func TestReadmeLicenseFallback(t *testing.T) {
	dir := t.TempDir()
	readme := "Package notes.\n\nPermission is hereby granted, free of charge, to any person\n"
	if err := os.WriteFile(filepath.Join(dir, "Readme"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readLicenseText(dir)
	if err != nil {
		t.Fatal(err)
	}
	if detectLicense(got) != "MIT" {
		t.Fatalf("readme license: %q", detectLicense(got))
	}
}

func TestFenceLicense(t *testing.T) {
	got := fenceLicense("## COPYING ##\n```\nMPL\n```\n")
	if !strings.HasPrefix(got, "````\n") || !strings.HasSuffix(got, "\n````\n") {
		t.Fatalf("fence: %q", got)
	}
}

func names(comps []Component) string {
	var b strings.Builder
	for _, c := range comps {
		if b.Len() > 0 {
			b.WriteString(",")
		}
		b.WriteString(c.Name)
		b.WriteString("@")
		b.WriteString(c.Version)
	}
	return b.String()
}

func hasName(comps []Component, name string) bool {
	for _, c := range comps {
		if c.Name == name {
			return true
		}
	}
	return false
}
