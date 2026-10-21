package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	runtimeTag = "ai-inference-onnx-runtime:engine-test"
	engineTag  = "ai-inference-onnx:engine-test"
)

// cpuLock is the host-free recorded runtime line: tag and digest, with no registry.
var cpuLock = regexp.MustCompile(`^cpu (1\.31\.0-cpu@sha256:[0-9a-f]{64})$`)

// resolveImage returns the engine image to run.
// ENGINE_TEST_IMAGE is used as given. Otherwise the image is built for the
// daemon's native amd64 or arm64 platform and is not pushed.
// recorded uses the cpu line of the runtime lock. source builds that runtime first.
// With neither set, a cpu lock line selects recorded and a missing line selects source.
func resolveImage(root string) (string, error) {
	if image := strings.TrimSpace(os.Getenv("ENGINE_TEST_IMAGE")); image != "" {
		return image, nil
	}
	platform, err := nativePlatform()
	if err != nil {
		return "", err
	}
	mode, ref, err := runtimeChoice(root)
	if err != nil {
		return "", err
	}
	switch mode {
	case "source":
		note("building the CPU runtime from source for %s\n", platform)
		if err := dockerBuild(root, []string{
			"buildx", "build",
			"-f", "build/onnxruntime/Dockerfile.cpu",
			"--target", "runtime",
			"--platform", platform,
			"--load",
			"-t", runtimeTag,
			".",
		}); err != nil {
			return "", err
		}
		ref = runtimeTag
	case "recorded":
		note("building the CPU engine from the recorded runtime for %s\n", platform)
	default:
		return "", fmt.Errorf("runtime choice %q", mode)
	}
	if err := dockerBuild(root, []string{
		"buildx", "build",
		"-f", "build/Dockerfile.cpu",
		"--target", "final-ubi",
		"--platform", platform,
		"--build-context", "onnxruntime=docker-image://" + ref,
		"--load",
		"-t", engineTag,
		".",
	}); err != nil {
		return "", err
	}
	return engineTag, nil
}

func runtimeChoice(root string) (mode, ref string, err error) {
	mode = strings.TrimSpace(os.Getenv("ENGINE_TEST_RUNTIME"))
	line, ok, err := cpuLockLine(filepath.Join(root, "build", "onnxruntime", "runtime-images.txt"))
	if err != nil {
		return "", "", err
	}
	switch mode {
	case "":
		if ok {
			mode = "recorded"
		} else {
			mode = "source"
		}
	case "recorded", "source":
	default:
		return "", "", errors.New("ENGINE_TEST_RUNTIME must be recorded or source")
	}
	if mode == "source" {
		return mode, "", nil
	}
	if !ok {
		return "", "", errors.New("recorded runtime: build/onnxruntime/runtime-images.txt has no cpu line")
	}
	registry := strings.TrimSpace(os.Getenv("IMAGE_REGISTRY"))
	if registry == "" {
		registry = "ghcr.io/eclipse-iofog"
	}
	registry = strings.TrimRight(registry, "/")
	return mode, registry + "/ai-inference-onnx-runtime:" + line, nil
}

func cpuLockLine(path string) (string, bool, error) {
	f, err := os.Open(path) // #nosec G304 -- the path is the runtime lock in the module tree, not a request path
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	defer f.Close()
	var found string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := cpuLock.FindStringSubmatch(line)
		if m == nil {
			if strings.HasPrefix(line, "cpu ") || strings.HasPrefix(line, "cpu\t") {
				return "", false, fmt.Errorf("malformed runtime lock line %q", line)
			}
			continue
		}
		if found != "" {
			return "", false, errors.New("runtime lock has more than one cpu line")
		}
		found = m[1]
	}
	if err := s.Err(); err != nil {
		return "", false, err
	}
	if found == "" {
		return "", false, nil
	}
	return found, true, nil
}

func nativePlatform() (string, error) {
	out, err := dockerCmd("info", "-f", "{{.Architecture}}").Output()
	if err != nil {
		return "", fmt.Errorf("docker info: %w", err)
	}
	switch strings.TrimSpace(string(out)) {
	case "x86_64", "amd64":
		return "linux/amd64", nil
	case "aarch64", "arm64":
		return "linux/arm64", nil
	default:
		return "", fmt.Errorf("the Docker daemon architecture %s is not amd64 or arm64", strings.TrimSpace(string(out)))
	}
}

func dockerBuild(root string, args []string) error {
	cmd := dockerCmd(args...)
	cmd.Dir = root
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		info, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil && !info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", dir)
		}
		dir = parent
	}
}
