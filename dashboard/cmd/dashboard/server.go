package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/dashboard/internal/page"
	"github.com/eclipse-iofog/ai-inference-onnx/dashboard/internal/store"
)

// app serves the nodes, models, and engine pages, and the health check.
// Health means the process can answer. It does not mean snapshots are arriving.
type app struct {
	logger    *slog.Logger
	snapshots *store.Store
	leaf      *subscriber
}

func newApp(logger *slog.Logger, snapshots *store.Store, leaf *subscriber) *app {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if snapshots == nil {
		snapshots = store.New(nil)
	}
	return &app{logger: logger, snapshots: snapshots, leaf: leaf}
}

// handler routes the pages.
// Any method other than GET on those paths is 405. Any other path is 404.
func (a *app) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", getOnly(a.nodes))
	mux.HandleFunc("/nodes", getOnly(a.nodes))
	mux.HandleFunc("/models", getOnly(a.models))
	mux.HandleFunc("/engines/{serverID}", getOnly(a.engine))
	mux.HandleFunc("/health", getOnly(a.health))
	return logRequests(a.logger, mux)
}

func (a *app) nodes(w http.ResponseWriter, _ *http.Request) {
	a.writePage(w, "nodes", func() error {
		return page.Nodes(w, a.snapshots, a.leaf.status())
	})
}

func (a *app) models(w http.ResponseWriter, _ *http.Request) {
	a.writePage(w, "models", func() error {
		return page.Models(w, a.snapshots, a.leaf.status())
	})
}

func (a *app) engine(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("serverID")
	model := r.URL.Query().Get("model")
	a.writePage(w, "engine", func() error {
		return page.EngineModel(w, a.snapshots, serverID, model, a.leaf.status())
	})
}

func (a *app) health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (a *app) writePage(w http.ResponseWriter, name string, render func() error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := render(); err != nil {
		a.logger.Error(fmt.Sprintf("render %s: %v", name, err))
	}
}

func getOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}

func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if logger == nil {
			return
		}
		logger.Debug("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_us", time.Since(start).Microseconds(),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.status = status
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.wrote = true
	}
	return w.ResponseWriter.Write(p)
}
