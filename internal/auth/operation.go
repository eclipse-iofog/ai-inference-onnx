package auth

import (
	"net/http"
	"strings"
)

// operation is one protected call, or an exemption such as health.
type operation struct {
	resource string
	verb     string
	known    bool
	exempt   bool
}

func operationFromHTTP(method, rawPath string) operation {
	path := normalizePath(rawPath)
	if path == "/metrics" || path == "/v2/health/live" || path == "/v2/health/ready" {
		return operation{exempt: true}
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	switch {
	case method == http.MethodGet && path == "/v2":
		return operation{resource: "server", verb: "get", known: true}
	case method == http.MethodGet && path == "/v2/models":
		return operation{resource: "models", verb: "get", known: true}
	case method == http.MethodPost && path == "/v2/repository/index":
		return operation{resource: "repository/index", verb: "get", known: true}
	default:
		return modelOperation(method, path)
	}
}

func modelOperation(method, path string) operation {
	const prefix = "/v2/models/"
	if !strings.HasPrefix(path, prefix) {
		return operation{}
	}
	parts := strings.Split(path[len(prefix):], "/")
	if len(parts) == 0 || !validModelName(parts[0]) {
		return operation{}
	}
	name := parts[0]
	action, ok := modelAction(parts)
	if !ok {
		return operation{}
	}
	switch {
	case method == http.MethodGet && (action == "metadata" || action == "ready"):
		return operation{resource: "models/" + name, verb: "get", known: true}
	case method == http.MethodPost && action == "infer":
		return operation{resource: "models/" + name + "/infer", verb: "create", known: true}
	default:
		return operation{}
	}
}

// modelAction reports metadata, ready, or infer.
// The versions segment selects a model version and is not part of the model name.
func modelAction(parts []string) (string, bool) {
	switch len(parts) {
	case 1:
		return "metadata", true
	case 2:
		if parts[1] == "ready" || parts[1] == "infer" {
			return parts[1], true
		}
		return "", false
	case 3:
		if parts[1] != "versions" || parts[2] == "" {
			return "", false
		}
		return "metadata", true
	case 4:
		if parts[1] != "versions" || parts[2] == "" {
			return "", false
		}
		if parts[3] == "ready" || parts[3] == "infer" {
			return parts[3], true
		}
		return "", false
	default:
		return "", false
	}
}

func operationFromGRPC(fullMethod, model string) operation {
	switch methodName(fullMethod) {
	case "ServerLive", "ServerReady":
		return operation{exempt: true}
	case "ServerMetadata":
		return operation{resource: "server", verb: "get", known: true}
	case "RepositoryIndex":
		return operation{resource: "repository/index", verb: "get", known: true}
	case "ModelMetadata", "ModelReady":
		if !validModelName(model) {
			return operation{}
		}
		return operation{resource: "models/" + model, verb: "get", known: true}
	case "ModelInfer":
		if !validModelName(model) {
			return operation{}
		}
		return operation{resource: "models/" + model + "/infer", verb: "create", known: true}
	default:
		return operation{}
	}
}

func methodName(fullMethod string) string {
	fullMethod = strings.TrimSpace(fullMethod)
	if slash := strings.LastIndex(fullMethod, "/"); slash >= 0 {
		return fullMethod[slash+1:]
	}
	return fullMethod
}

func validModelName(name string) bool {
	return name != "" && !strings.Contains(name, "/")
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if query := strings.IndexByte(path, '?'); query >= 0 {
		path = path[:query]
	}
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return path
}
