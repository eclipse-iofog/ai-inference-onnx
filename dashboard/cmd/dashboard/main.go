// Command dashboard serves the nodes, models, and engine pages.
// It listens for engine snapshots when both the NATS server URL and the credentials path are set.
// If either is unset, it stays up and the pages say that NATS is not configured.
// A server that cannot be reached is retried in the background and does not stop the process.
// Invalid settings or a port that cannot be bound stop the process before it finishes starting.
// The process writes one JSON log to standard output.
// It does not read the engine configuration file.
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
	"strconv"
	"syscall"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/dashboard/internal/store"
)

const stopTimeout = 5 * time.Second

func main() {
	os.Exit(run())
}

func run() int {
	logger := newLogger(os.Stdout, "info")
	cfg, err := loadConfig()
	if err != nil {
		logger.Error(fmt.Sprintf("configuration: %v", err))
		return 1
	}
	logger = newLogger(os.Stdout, cfg.LogLevel)
	tlsConfig, err := loadListenerTLS(cfg)
	if err != nil {
		logger.Error(fmt.Sprintf("configuration: %v", err))
		return 1
	}

	snapshots := store.New(nil)
	leaf := startSubscriber(cfg, snapshots, logger)
	defer leaf.Close()

	ln, err := net.Listen("tcp", listenAddr(cfg.Port))
	if err != nil {
		logger.Error(fmt.Sprintf("listen: %v", err))
		return 1
	}
	logger.Info("listener is up", "addr", ln.Addr().String())

	server := &http.Server{
		Handler:           newApp(logger, snapshots, leaf).handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          httpErrorLog(logger),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- serve(server, ln, tlsConfig)
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case <-signals:
		return shutdown(logger, server)
	case err := <-errCh:
		code := shutdown(logger, server)
		if err != nil {
			logger.Error(fmt.Sprintf("serve: %v", err))
			return 1
		}
		return code
	}
}

// listenAddr binds every interface on port.
func listenAddr(port int) string {
	return net.JoinHostPort("", strconv.Itoa(port))
}

// serve accepts connections on ln.
// A nil tlsConfig serves plain HTTP. Otherwise the listener presents the certificate in tlsConfig.
func serve(server *http.Server, ln net.Listener, tlsConfig *tls.Config) error {
	if tlsConfig != nil {
		ln = tls.NewListener(ln, tlsConfig)
	}
	err := server.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func shutdown(logger *slog.Logger, server *http.Server) int {
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error(fmt.Sprintf("stop: %v", err))
		return 1
	}
	return 0
}
