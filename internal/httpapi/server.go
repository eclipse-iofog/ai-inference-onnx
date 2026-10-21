// Package httpapi serves health checks, model metadata, the repository index, and infer.
// Every path except live and ready requires authentication.
// The listener is plain HTTP unless a certificate is configured.
package httpapi

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/auth"
)

const (
	errorUnauthenticated  = `{"error":"unauthenticated"}`
	errorForbidden        = `{"error":"forbidden"}`
	errorNotFound         = `{"error":"not found"}`
	errorMethodNotAllowed = `{"error":"method not allowed"}`

	pathLive  = "/v2/health/live"
	pathReady = "/v2/health/ready"
)

// ListenAddr returns the listen address for port.
// The host is empty, so the listener accepts every interface.
func ListenAddr(port int) string {
	return net.JoinHostPort("", strconv.Itoa(port))
}

// Server answers health checks and applies authentication to every other path.
type Server struct {
	checker *auth.Checker
	ready   func() bool
	catalog Catalog
	runner  Runner
	payload int
	logger  *slog.Logger
	mux     *http.ServeMux
}

// New returns a server that uses checker for protected paths.
// ready reports whether any model can serve. A nil ready func means not ready.
func New(checker *auth.Checker, ready func() bool) *Server {
	s := &Server{checker: checker, ready: ready}
	mux := http.NewServeMux()
	mux.HandleFunc(pathLive, s.handleLive)
	mux.HandleFunc(pathReady, s.handleReady)
	s.mux = mux
	return s
}

// ServeHTTP checks the caller, then dispatches a health check or an error.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	decision, err := s.checker.AuthorizeHTTP(r.Method, r.URL.Path, r.Header.Get("Authorization"), time.Now())
	if err != nil || decision != auth.Allow {
		if decision == auth.Forbidden {
			writeJSON(w, http.StatusForbidden, errorForbidden)
			return
		}
		writeJSON(w, http.StatusUnauthorized, errorUnauthenticated)
		return
	}
	s.dispatch(w, r)
}

func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	handler, pattern := s.mux.Handler(r)
	if pattern == "" {
		writeJSON(w, http.StatusNotFound, errorNotFound)
		return
	}
	handler.ServeHTTP(w, r)
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	if s.readyNow() {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusBadRequest)
}

func (s *Server) readyNow() bool {
	if s.ready == nil {
		return false
	}
	return s.ready()
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	writeAllowed(w, http.MethodGet)
}

func writeAllowed(w http.ResponseWriter, method string) {
	w.Header().Set("Allow", method)
	writeJSON(w, http.StatusMethodNotAllowed, errorMethodNotAllowed)
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := io.WriteString(w, body); err != nil {
		return
	}
}
