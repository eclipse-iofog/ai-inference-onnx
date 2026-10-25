package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	engineModule    = "github.com/eclipse-iofog/ai-inference-onnx"
	dashboardModule = "github.com/eclipse-iofog/ai-inference-onnx/dashboard"
	ortGoModule     = "github.com/microsoft/onnxruntime/go"
)

type resolvedMod struct {
	ImportPath string
	Path       string
	Version    string
	Dir        string
}

type closureSpec struct {
	Name  string
	Dir   string
	Env   []string
	Pkgs  []string
	Test  bool
	Allow bool
}

func ensureModules(dir string) error {
	cmd := exec.Command("go", "mod", "download", "all")
	cmd.Dir = dir
	cmd.Env = filteredEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go mod download in %s: %w\n%s", dir, err, stderr.String())
	}
	return nil
}

func listModules(dir string) (map[string]resolvedMod, error) {
	raw, err := runGo(dir, nil, "list", "-m", "-json", "all")
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	out := map[string]resolvedMod{}
	for dec.More() {
		var row struct {
			Path    string
			Version string
			Dir     string
			Main    bool
			Replace *struct {
				Path    string
				Version string
				Dir     string
			}
		}
		if err := dec.Decode(&row); err != nil {
			return nil, err
		}
		if row.Main {
			continue
		}
		mod := resolvedMod{ImportPath: row.Path, Path: row.Path, Version: row.Version, Dir: row.Dir}
		if row.Replace != nil && row.Replace.Path != "" {
			mod.Path = row.Replace.Path
			if row.Replace.Version != "" {
				mod.Version = row.Replace.Version
			}
			if row.Replace.Dir != "" {
				mod.Dir = row.Replace.Dir
			}
		}
		if mod.Dir == "" {
			return nil, fmt.Errorf("module %s has no downloaded dir", row.Path)
		}
		out[row.Path] = mod
	}
	return out, nil
}

func modulePaths(dir string, spec closureSpec) (map[string]struct{}, error) {
	args := []string{"list", "-deps", "-f", "{{if .Module}}{{.Module.Path}}{{end}}"}
	if spec.Allow {
		args = []string{"list", "-e", "-deps", "-f", "{{if .Module}}{{.Module.Path}}{{end}}"}
	}
	if spec.Test {
		args = append(args, "-test")
	}
	args = append(args, spec.Pkgs...)
	raw, err := runGo(dir, spec.Env, args...)
	if err != nil {
		return nil, fmt.Errorf("closure %s: %w", spec.Name, err)
	}
	out := map[string]struct{}{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out[line] = struct{}{}
		}
	}
	return out, nil
}

func productClosures() []closureSpec {
	return []closureSpec{
		{Name: "engine", Env: []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=1"}, Pkgs: []string{"./cmd/inference"}},
		{Name: "dashboard", Dir: "dashboard", Env: []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0"}, Pkgs: []string{"./..."}},
	}
}

func testClosures() []closureSpec {
	return []closureSpec{
		{Name: "test-engine", Env: []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=1"}, Pkgs: []string{"./..."}, Test: true, Allow: true},
		{Name: "test-dashboard", Dir: "dashboard", Env: []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0"}, Pkgs: []string{"./..."}, Test: true, Allow: true},
	}
}

func runGo(dir string, extra []string, args ...string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = filteredEnv()
	cmd.Env = append(cmd.Env, "GOFLAGS=-mod=readonly")
	cmd.Env = append(cmd.Env, extra...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func filteredEnv() []string {
	var out []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "GOFLAGS=") || strings.HasPrefix(e, "GOOS=") || strings.HasPrefix(e, "GOARCH=") || strings.HasPrefix(e, "CGO_ENABLED=") {
			continue
		}
		out = append(out, e)
	}
	return out
}

func golangPURL(path, version string) string {
	return "pkg:golang/" + path + "@" + strings.ReplaceAll(version, "+", "%2B")
}

func mainModule(path string) bool {
	return path == engineModule || path == dashboardModule
}
