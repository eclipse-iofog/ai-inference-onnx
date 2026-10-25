package main

import "fmt"

func runtimeClosureNames() []string {
	return []string{
		"runtime-cpu",
		"runtime-cuda12",
		"runtime-cuda13",
		"runtime-migraphx",
		"runtime-openvino",
		"runtime-qnn",
		"runtime-tensorrt12",
		"runtime-tensorrt13-amd64",
		"runtime-tensorrt13-arm64",
	}
}

func closureFor(variant, arch string) (string, error) {
	switch variant {
	case "cpu", "cuda12", "cuda13", "tensorrt12", "qnn", "openvino", "migraphx":
		if err := archAllowed(variant, arch); err != nil {
			return "", err
		}
		return "runtime-" + variant, nil
	case "tensorrt13":
		if arch != "amd64" && arch != "arm64" {
			return "", fmt.Errorf("tensorrt13 has no %s runtime", arch)
		}
		return "runtime-tensorrt13-" + arch, nil
	default:
		return "", fmt.Errorf("unknown variant %s", variant)
	}
}

func archAllowed(variant, arch string) error {
	switch variant {
	case "cpu":
		if arch == "amd64" || arch == "arm64" || arch == "riscv64" {
			return nil
		}
	case "cuda12", "cuda13":
		if arch == "amd64" || arch == "arm64" {
			return nil
		}
	case "tensorrt12", "openvino", "migraphx":
		if arch == "amd64" {
			return nil
		}
	case "qnn":
		if arch == "arm64" {
			return nil
		}
	}
	return fmt.Errorf("%s has no %s runtime", variant, arch)
}

func runtimeComponents(repo string, pins Pins) ([]Component, error) {
	cuda, err := readNotice(repo, "nvidia-cuda.txt")
	if err != nil {
		return nil, err
	}
	cudnn, err := readNotice(repo, "nvidia-cudnn.txt")
	if err != nil {
		return nil, err
	}
	tensorrt, err := readNotice(repo, "nvidia-tensorrt.txt")
	if err != nil {
		return nil, err
	}
	migraphx, err := readNotice(repo, "migraphx-mit.txt")
	if err != nil {
		return nil, err
	}
	qnn, err := readNotice(repo, "qualcomm-qnn.txt")
	if err != nil {
		return nil, err
	}

	byRef := map[string]*Component{}
	var order []string
	add := func(c Component) {
		if prev, ok := byRef[c.Ref]; ok {
			prev.Closures = uniqueStrings(append(prev.Closures, c.Closures...))
			return
		}
		cp := c
		byRef[c.Ref] = &cp
		order = append(order, c.Ref)
	}
	add(Component{
		Ref:      "pkg:generic/onnxruntime@" + pins.ORTVersion,
		Type:     "library",
		Name:     "ONNX Runtime",
		Version:  pins.ORTVersion,
		PURL:     "pkg:generic/onnxruntime@" + pins.ORTVersion,
		Scope:    scopeRequired,
		License:  "MIT",
		Closures: runtimeClosureNames(),
		Relation: relDepends,
		Props: []Prop{
			prop(propCommit, pins.ORTCommit),
			prop(propStagedLibrary, "libonnxruntime.so."+pins.ORTVersion),
			prop(propStagedImage, imageBoth),
		},
		Note: "Shared library built from " + pins.ORTRepo + " commit " + pins.ORTCommit + ". The engine loads libonnxruntime.so." + pins.ORTVersion + " at run time and does not link it. licenses/LICENSE and licenses/ThirdPartyNotices.txt ship in the runtime image.",
	})
	addSDK := func(name, version, license, text, library, image string, closures []string, note string) {
		add(Component{
			Ref:         "pkg:generic/" + purlName(name) + "@" + version,
			Type:        "library",
			Name:        name,
			Version:     version,
			PURL:        "pkg:generic/" + purlName(name) + "@" + version,
			Scope:       scopeRequired,
			License:     license,
			LicenseText: text,
			Closures:    closures,
			Relation:    relDepends,
			Props: []Prop{
				prop(propStagedLibrary, library),
				prop(propStagedImage, image),
			},
			Note: note,
		})
	}
	cudaNote := "NVIDIA CUDA runtime staged into the engine image. The license text is the CUDA redistributable license, also staged as /licenses/cuda-cudart-LICENSE."
	cudnnNote := "NVIDIA cuDNN staged into the engine image. The license text is the cuDNN redistributable LICENSE, including the NVIDIA SDK agreement and the third-party notices in that file."
	trtNote := "NVIDIA TensorRT staged into the engine image. The license text is the TensorRT SDK agreement for the shipped library."
	addSDK("CUDA", pins.CUDA12, "LicenseRef-NVIDIA-CUDA", cuda, "libcudart.so", imageEngine,
		[]string{"runtime-cuda12"}, cudaNote)
	addSDK("CUDA", pins.CUDA13, "LicenseRef-NVIDIA-CUDA", cuda, "libcudart.so", imageEngine,
		[]string{"runtime-cuda13", "runtime-tensorrt13-amd64"}, cudaNote)
	addSDK("CUDA", pins.TRT12CUDA, "LicenseRef-NVIDIA-CUDA", cuda, "libcudart.so", imageEngine,
		[]string{"runtime-tensorrt12"}, cudaNote)
	addSDK("CUDA", pins.TRT13ARM64CUDA, "LicenseRef-NVIDIA-CUDA", cuda, "libcudart.so", imageEngine,
		[]string{"runtime-tensorrt13-arm64"}, cudaNote)
	addSDK("cuDNN", pins.CUDNN12, "LicenseRef-NVIDIA-cuDNN", cudnn, "libcudnn.so", imageEngine,
		[]string{"runtime-cuda12"}, cudnnNote)
	addSDK("cuDNN", pins.CUDNN13, "LicenseRef-NVIDIA-cuDNN", cudnn, "libcudnn.so", imageEngine,
		[]string{"runtime-cuda13", "runtime-tensorrt13-amd64"}, cudnnNote)
	addSDK("cuDNN", pins.TRT12CUDNN, "LicenseRef-NVIDIA-cuDNN", cudnn, "libcudnn.so", imageEngine,
		[]string{"runtime-tensorrt12"}, cudnnNote)
	addSDK("cuDNN", pins.TRT13ARM64CUDNN, "LicenseRef-NVIDIA-cuDNN", cudnn, "libcudnn.so", imageEngine,
		[]string{"runtime-tensorrt13-arm64"}, cudnnNote)
	addSDK("TensorRT", pins.TRT12, "LicenseRef-NVIDIA-TensorRT", tensorrt, "libnvinfer.so", imageEngine,
		[]string{"runtime-tensorrt12"}, trtNote)
	addSDK("TensorRT", pins.TRT13AMD64, "LicenseRef-NVIDIA-TensorRT", tensorrt, "libnvinfer.so", imageEngine,
		[]string{"runtime-tensorrt13-amd64"}, trtNote)
	addSDK("TensorRT", pins.TRT13ARM64, "LicenseRef-NVIDIA-TensorRT", tensorrt, "libnvinfer.so", imageEngine,
		[]string{"runtime-tensorrt13-arm64"}, trtNote)
	addSDK("Qualcomm QNN", pins.QNN, "LicenseRef-qcom-ai-stack", qnn, "libQnnHtp.so", imageBoth,
		[]string{"runtime-qnn"},
		"Qualcomm QNN HTP backend. The libraries are copied into the ONNX Runtime library directory. The license is the Qualcomm AI Stack license, staged as /licenses/Qualcomm_LICENSE.pdf.")
	addSDK("OpenVINO", pins.OpenVINO, "Apache-2.0", "", "libopenvino.so", imageEngine,
		[]string{"runtime-openvino"},
		"Intel OpenVINO staged into the engine image. The license file is /licenses/openvino-LICENSE.")
	addSDK("AMD MIGraphX", pins.ROCm, "MIT", migraphx, "libmigraphx_c.so", imageEngine,
		[]string{"runtime-migraphx"},
		"AMD MIGraphX on ROCm, staged under /opt/rocm in the engine image. The license is the MIT text from the ROCm tree, and those license files are copied to /licenses.")

	out := make([]Component, 0, len(order))
	for _, ref := range order {
		out = append(out, *byRef[ref])
	}
	return out, nil
}

func purlName(name string) string {
	switch name {
	case "CUDA":
		return "cuda"
	case "cuDNN":
		return "cudnn"
	case "TensorRT":
		return "tensorrt"
	case "Qualcomm QNN":
		return "qnn"
	case "OpenVINO":
		return "openvino"
	case "AMD MIGraphX":
		return "migraphx"
	default:
		return name
	}
}
