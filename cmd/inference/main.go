// Command inference loads server settings and serves HTTP and gRPC.
// HTTP serves health checks, server metadata, the model list, model metadata, model ready, the repository index, and infer.
// gRPC serves health checks, server metadata, model metadata, model ready, the repository index, and infer.
// The metrics port serves GET /metrics as plain HTTP and does not require a caller token.
// That listener does not present the REST certificate.
// When both the NATS server URL and the credentials path are set, the same calls are also served on NATS.
// A NATS server that cannot be reached is retried in the background and does not stop the process.
// Invalid settings or an unusable Edgelet signing key stop the process before it listens.
// When the runtime library is available, model sessions start from the model and knowledge directories.
// A signal stops listeners, closes those sessions, and then releases the runtime.
// The process writes one JSON log to standard output.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/auth"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/grpcapi"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/httpapi"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/metrics"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/natsapi"
)

var _ httpapi.Catalog = (*modelRuntime)(nil)

const (
	// headerTimeout bounds how long a connection may sit before sending headers.
	headerTimeout = 10 * time.Second
	// stopTimeout bounds how long in-flight health checks may finish after a signal.
	stopTimeout = 5 * time.Second
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := newLogger(os.Stdout, "info")
	cfg, err := config.Load()
	if err != nil {
		logger.Error(fmt.Sprintf("configuration: %v", err))
		return 1
	}
	logger = newLogger(os.Stdout, cfg.LogLevel)
	checker, err := auth.New(cfg, "")
	if err != nil {
		logger.Error(fmt.Sprintf("authentication: %v", err))
		return 1
	}
	tlsConfig, err := loadListenerTLS(cfg)
	if err != nil {
		logger.Error(fmt.Sprintf("configuration: %v", err))
		return 1
	}

	// notes receives a copy of the model list after a state is stored.
	notes := make(chan []metrics.ModelState, 16)
	models := startModels(cfg, logger, notes)
	api := httpapi.New(checker, models.ready)
	api.RegisterReads(models)
	api.RegisterInfer(httpapi.InferConfig{
		Catalog:         models,
		Runner:          models,
		MaxPayloadBytes: cfg.MaxPayloadBytes,
		Logger:          logger,
	})
	httpServer := &http.Server{
		Handler:           api,
		ReadHeaderTimeout: headerTimeout,
		ErrorLog:          httpErrorLog(logger),
	}
	grpcServer := newGRPCServer(checker, models.ready, tlsConfig)
	grpcServer.RegisterReads(models)
	grpcServer.RegisterInfer(grpcapi.InferConfig{
		Catalog:         models,
		Runner:          models,
		MaxPayloadBytes: cfg.MaxPayloadBytes,
		Logger:          logger,
	})

	// registry is the one set of counters for this process.
	// The metrics listener and the snapshot read it.
	registry := metrics.New()
	models.useRegistry(registry)
	metricsServer := &http.Server{
		Handler:           newMetricsHandler(registry),
		ReadHeaderTimeout: headerTimeout,
		ErrorLog:          httpErrorLog(logger),
	}

	httpLn, grpcLn, metricsLn, err := listen(cfg)
	if err != nil {
		logger.Error(fmt.Sprintf("listen: %v", err))
		if stopErr := models.shutdown(); stopErr != nil {
			logger.Error(fmt.Sprintf("stop: %v", stopErr))
		}
		return 1
	}
	logger.Info("http listener is up", "addr", httpLn.Addr().String())
	logger.Info("grpc listener is up", "addr", grpcLn.Addr().String())
	logger.Info("metrics listener is up", "addr", metricsLn.Addr().String())

	natsCtx, cancelNATS := context.WithCancel(context.Background())
	defer cancelNATS()
	snapCtx, cancelSnap := context.WithCancel(context.Background())
	defer cancelSnap()
	var leaf atomic.Pointer[natsapi.Listener]
	go metrics.NewPublisher(metrics.PublisherConfig{
		Registry:         registry,
		Interval:         cfg.MetricsInterval,
		ServerID:         cfg.ServerID,
		MicroserviceName: cfg.MicroserviceName,
		NodeUID:          cfg.NodeUID,
		ApplicationName:  cfg.ApplicationName,
		Live:             func() bool { return true },
		Ready:            models.ready,
		Connected:        func() bool { return leaf.Load().Connected() },
		Publish: func(subject string, body []byte) error {
			return leaf.Load().Publish(subject, body)
		},
		Models: models.modelStates,
		Notes:  notes,
	}).Run(snapCtx)
	bus := startNATS(natsCtx, cfg, models, logger)
	leaf.Store(bus)
	stopAll := func() int {
		cancelSnap()
		cancelNATS()
		bus.Close()
		return finish(logger, httpServer, metricsServer, grpcServer, models)
	}

	errCh := make(chan error, 3)
	go func() {
		errCh <- serveHTTP(httpServer, httpLn, tlsConfig)
	}()
	go func() {
		errCh <- serveGRPC(grpcServer, grpcLn)
	}()
	go func() {
		errCh <- serveMetrics(metricsServer, metricsLn)
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case <-signals:
		return stopAll()
	case err := <-errCh:
		code := stopAll()
		if err != nil {
			logger.Error(fmt.Sprintf("serve: %v", err))
			return 1
		}
		return code
	}
}

// finish stops listeners, lets an in-flight run finish, closes sessions, and then releases the runtime.
// A shutdown error is logged and the process exits.
func finish(logger *slog.Logger, httpServer *http.Server, metricsServer *http.Server, grpcServer *grpcapi.Server, models *modelRuntime) int {
	err := stop(httpServer, metricsServer, grpcServer)
	if modelsErr := models.shutdown(); modelsErr != nil {
		err = errors.Join(err, modelsErr)
	}
	if err != nil {
		logger.Error(fmt.Sprintf("stop: %v", err))
		return 1
	}
	return 0
}

func listen(cfg config.Config) (net.Listener, net.Listener, net.Listener, error) {
	httpLn, err := net.Listen("tcp", httpapi.ListenAddr(cfg.HTTPPort))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("http %s: %w", httpapi.ListenAddr(cfg.HTTPPort), err)
	}
	grpcLn, err := net.Listen("tcp", grpcapi.ListenAddr(cfg.GRPCPort))
	if err != nil {
		return nil, nil, nil, joinClose(fmt.Errorf("grpc %s: %w", grpcapi.ListenAddr(cfg.GRPCPort), err), httpLn)
	}
	metricsLn, err := net.Listen("tcp", httpapi.ListenAddr(cfg.MetricsPort))
	if err != nil {
		return nil, nil, nil, joinClose(fmt.Errorf("metrics %s: %w", httpapi.ListenAddr(cfg.MetricsPort), err), httpLn, grpcLn)
	}
	return httpLn, grpcLn, metricsLn, nil
}

// joinClose closes listeners and returns err together with any close error.
func joinClose(err error, listeners ...net.Listener) error {
	for _, ln := range listeners {
		if closeErr := ln.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}
	return err
}

func newGRPCServer(checker *auth.Checker, ready func() bool, tlsConfig *tls.Config) *grpcapi.Server {
	if tlsConfig == nil {
		return grpcapi.New(checker, ready)
	}
	return grpcapi.NewTLS(checker, ready, tlsConfig)
}

func serveHTTP(server *http.Server, ln net.Listener, tlsConfig *tls.Config) error {
	err := httpapi.Serve(server, ln, tlsConfig)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func serveGRPC(server *grpcapi.Server, ln net.Listener) error {
	err := server.Serve(ln)
	if errors.Is(err, grpc.ErrServerStopped) {
		return nil
	}
	return err
}

func stop(httpServer *http.Server, metricsServer *http.Server, grpcServer *grpcapi.Server) error {
	grpcServer.GracefulStop()
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	return errors.Join(shutdownServer(ctx, "http", httpServer), shutdownServer(ctx, "metrics", metricsServer))
}

func shutdownServer(ctx context.Context, name string, server *http.Server) error {
	if err := server.Shutdown(ctx); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
