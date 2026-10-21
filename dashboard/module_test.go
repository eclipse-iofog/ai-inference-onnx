package dashboard

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
)

const (
	engineModule    = "github.com/eclipse-iofog/ai-inference-onnx"
	dashboardModule = engineModule + "/dashboard"
)

// TestModulePath fails when the dashboard module path is wrong.
func TestModulePath(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("build info unavailable")
	}
	if info.Main.Path != dashboardModule {
		t.Fatalf("module path %q, want %q", info.Main.Path, dashboardModule)
	}
}

// TestDashboardTree fails when dashboard code outside the listener serves HTTP,
// or when any file imports ONNX Runtime or the engine module.
// The listener command may serve HTTP.
func TestDashboardTree(t *testing.T) {
	root := moduleRoot(t)
	var problems []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		found, inspectErr := inspectFile(path)
		if inspectErr != nil {
			return inspectErr
		}
		problems = append(problems, found...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk dashboard tree: %v", err)
	}
	if len(problems) > 0 {
		t.Fatalf("dashboard tree:\n%s", strings.Join(problems, "\n"))
	}
}

// TestRejectedDashboardCode fails when a server or a forbidden import is not reported.
func TestRejectedDashboardCode(t *testing.T) {
	samples := []struct {
		name   string
		src    string
		reject bool
	}{
		{
			name: "listen",
			src: `package dashboard
import "net/http"
func main() { _ = http.ListenAndServe(":8080", nil) }
`,
			reject: true,
		},
		{
			name: "server type",
			src: `package dashboard
import "net/http"
func f() *http.Server { return nil }
`,
			reject: true,
		},
		{
			name: "test server",
			src: `package dashboard
import "net/http/httptest"
func f() { _ = httptest.NewServer(nil) }
`,
			reject: true,
		},
		{
			name: "onnx runtime",
			src: `package dashboard
import "github.com/yalue/onnxruntime_go"
func f() {}
`,
			reject: true,
		},
		{
			name: "engine module",
			src: `package dashboard
import "github.com/eclipse-iofog/ai-inference-onnx"
func f() {}
`,
			reject: true,
		},
		{
			name: "engine package",
			src: `package dashboard
import "github.com/eclipse-iofog/ai-inference-onnx/internal/config"
func f() {}
`,
			reject: true,
		},
		{
			name: "dashboard package",
			src: `package dashboard
import "github.com/eclipse-iofog/ai-inference-onnx/dashboard/internal/store"
func f() {}
`,
			reject: false,
		},
		{
			name: "http client",
			src: `package dashboard
import "net/http"
func f() { _, _ = http.Get("http://127.0.0.1") }
`,
			reject: false,
		},
	}
	for _, sample := range samples {
		t.Run(sample.name, func(t *testing.T) {
			problems, err := inspect("sample.go", []byte(sample.src))
			if err != nil {
				t.Fatalf("inspect: %v", err)
			}
			if sample.reject && len(problems) == 0 {
				t.Fatal("expected a problem")
			}
			if !sample.reject && len(problems) > 0 {
				t.Fatalf("unexpected problem: %s", strings.Join(problems, "; "))
			}
		})
	}
}

// TestDashboardCommandMayListen allows the listener command to serve HTTP.
// An ONNX Runtime import or an engine import in that command is still rejected.
func TestDashboardCommandMayListen(t *testing.T) {
	listen := `package main
import "net/http"
func main() { _ = http.ListenAndServe(":8080", nil) }
`
	problems, err := inspect("cmd/dashboard/main.go", []byte(listen))
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("listener: %s", strings.Join(problems, "; "))
	}

	runtimeImport := `package main
import "github.com/yalue/onnxruntime_go"
func main() {}
`
	problems, err = inspect("cmd/dashboard/main.go", []byte(runtimeImport))
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, " "), "ONNX Runtime") {
		t.Fatalf("runtime import: %s", strings.Join(problems, "; "))
	}

	engineImport := `package main
import "github.com/eclipse-iofog/ai-inference-onnx/internal/config"
func main() {}
`
	problems, err = inspect("/tmp/dashboard/cmd/dashboard/runtime.go", []byte(engineImport))
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, " "), "engine module") {
		t.Fatalf("engine import: %s", strings.Join(problems, "; "))
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	dir := filepath.Dir(filename)
	for {
		_, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stat module file: %v", err)
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				t.Fatal("dashboard module file not found")
			}
			dir = parent
			continue
		}
		return dir
	}
}

func inspectFile(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return inspect(path, body)
}

func inspect(filename string, src []byte) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}
	return problemsIn(filename, file), nil
}

func problemsIn(filename string, file *ast.File) []string {
	var problems []string
	for _, spec := range file.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s has an unreadable import path", filename))
			continue
		}
		if onnxRuntimeImport(imported) {
			problems = append(problems, fmt.Sprintf("%s imports ONNX Runtime %s", filename, imported))
		}
		if engineModuleImport(imported) {
			problems = append(problems, fmt.Sprintf("%s imports the engine module %s", filename, imported))
		}
	}
	if servesHTTP(file) && !dashboardCommand(filename) {
		problems = append(problems, filename+" starts an HTTP server")
	}
	return problems
}

// dashboardCommand reports whether filename is the listener command.
// That command is the process that serves the pages.
func dashboardCommand(filename string) bool {
	clean := filepath.ToSlash(filename)
	return strings.Contains("/"+clean, "/cmd/dashboard/")
}

func onnxRuntimeImport(path string) bool {
	return strings.Contains(strings.ToLower(path), "onnxruntime")
}

func engineModuleImport(path string) bool {
	if path == dashboardModule || strings.HasPrefix(path, dashboardModule+"/") {
		return false
	}
	return path == engineModule || strings.HasPrefix(path, engineModule+"/")
}

func servesHTTP(file *ast.File) bool {
	httpName, httpDot := importLocalName(file, "net/http", "http")
	testName, testDot := importLocalName(file, "net/http/httptest", "httptest")
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		if found {
			return false
		}
		switch n := node.(type) {
		case *ast.FuncDecl:
			if n.Name != nil && isListen(n.Name.Name) {
				found = true
			}
		case *ast.SelectorExpr:
			found = selectorServes(n, httpName, testName)
		case *ast.Ident:
			found = identServes(n.Name, httpDot, testDot)
		}
		return !found
	})
	return found
}

func importLocalName(file *ast.File, importPath, fallback string) (string, bool) {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		if spec.Name == nil {
			return fallback, false
		}
		switch spec.Name.Name {
		case ".":
			return "", true
		case "_":
			return "", false
		default:
			return spec.Name.Name, false
		}
	}
	return "", false
}

func selectorServes(sel *ast.SelectorExpr, httpName, testName string) bool {
	if isListen(sel.Sel.Name) {
		return true
	}
	pkg, ok := selectorPkg(sel)
	if !ok {
		return false
	}
	if httpName != "" && pkg == httpName && isHTTPServer(sel.Sel.Name) {
		return true
	}
	return testName != "" && pkg == testName && isHTTPTest(sel.Sel.Name)
}

func identServes(name string, httpDot, testDot bool) bool {
	if httpDot && (isListen(name) || isHTTPServer(name)) {
		return true
	}
	return testDot && (isListen(name) || isHTTPTest(name))
}

func selectorPkg(sel *ast.SelectorExpr) (string, bool) {
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	return id.Name, true
}

func isListen(name string) bool {
	return name == "ListenAndServe" || name == "ListenAndServeTLS"
}

func isHTTPServer(name string) bool {
	return name == "Server" || name == "Serve"
}

func isHTTPTest(name string) bool {
	switch name {
	case "NewServer", "NewUnstartedServer", "Server":
		return true
	default:
		return false
	}
}
