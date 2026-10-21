package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/eclipse-iofog/ai-inference-onnx/dashboard/internal/page"
	"github.com/eclipse-iofog/ai-inference-onnx/dashboard/internal/store"
)

// snapshotSubject is every engine snapshot.
// There is no queue group, so each listener receives every snapshot.
const snapshotSubject = "inference.metrics.engines.>"

// clientName is how this process names its connection.
const clientName = "dashboard"

// errCredentials marks a failed attempt that was already logged
// when the credentials or the CA file could not be read.
var errCredentials = errors.New("nats credentials")

// subscriber is the connection that receives engine snapshots.
// It dials in the background and keeps retrying for as long as the process runs.
// A server that cannot be reached, missing credentials, or a missing CA file do not stop the process.
// It does not publish.
type subscriber struct {
	configured bool
	conn       *nats.Conn
}

// startSubscriber dials when both the server URL and the credentials path are set.
// Otherwise it returns nil and nothing dials.
// A dial that cannot start is logged and the listener stays up, showing that it is not connected.
func startSubscriber(cfg config, snapshots *store.Store, logger *slog.Logger) *subscriber {
	if !cfg.natsOn() {
		return nil
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	sub, err := dialSubscriber(cfg, snapshots, logger)
	if err != nil {
		logger.Error(fmt.Sprintf("nats: %v", err))
		return &subscriber{configured: true}
	}
	return sub
}

// dialSubscriber connects in the background and subscribes to engine snapshots.
// It returns before the first connection is made. A failed attempt is retried without a limit,
// and the subscription is restored on every reconnect. The queue group is empty.
// An error means the settings cannot be used. It is not a connection failure.
func dialSubscriber(cfg config, snapshots *store.Store, logger *slog.Logger) (*subscriber, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	opts := []nats.Option{
		nats.Name(clientName),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		// A rejected credential is retried like any other failure, so a rotated file can be picked up.
		nats.IgnoreAuthErrorAbort(),
		credentials(cfg.NATSCredsPath, logger),
		nats.ConnectHandler(func(nc *nats.Conn) {
			logger.Info("nats connected", "server", nc.ConnectedAddr())
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			logger.Info("nats connected", "server", nc.ConnectedAddr())
		}),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			logger.Info("nats disconnected", "error", errorText(err))
		}),
		nats.ReconnectErrHandler(func(_ *nats.Conn, err error) {
			logger.Warn("nats connection attempt failed, retrying", "error", errorText(err))
		}),
		nats.ErrorHandler(func(_ *nats.Conn, sub *nats.Subscription, err error) {
			switch {
			case sub != nil:
				logger.Error("nats subscription failed", "subject", sub.Subject, "error", errorText(err))
			case errors.Is(err, errCredentials):
			default:
				logger.Warn("nats connection attempt failed, retrying", "error", errorText(err))
			}
		}),
		nats.ClosedHandler(func(*nats.Conn) {
			logger.Info("nats connection closed")
		}),
	}
	if secureURL(cfg.NATSServerURL) && cfg.NATSCAFile != "" {
		opts = append(opts, trustedCA(cfg.NATSCAFile, logger))
	}
	conn, err := nats.Connect(cfg.NATSServerURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats settings: %w", err)
	}
	_, err = conn.Subscribe(snapshotSubject, func(msg *nats.Msg) {
		if msg == nil || snapshots == nil {
			return
		}
		snapshots.Accept(msg.Data)
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("subscribe %s: %w", snapshotSubject, err)
	}
	logger.Info("nats listener is up", "server", redactURL(cfg.NATSServerURL))
	return &subscriber{configured: true, conn: conn}, nil
}

// status reports whether snapshots can arrive.
// A nil subscriber means the server URL or the credentials path is unset.
func (s *subscriber) status() page.Connection {
	if s == nil || !s.configured {
		return page.ConnectionNotConfigured
	}
	if s.conn != nil && s.conn.IsConnected() {
		return page.ConnectionUp
	}
	return page.ConnectionDown
}

// Close closes the connection.
// Subscriptions stop with it. A nil subscriber does nothing.
func (s *subscriber) Close() {
	if s == nil || s.conn == nil {
		return
	}
	s.conn.Close()
}

// credentials reads the user JWT and signs the server nonce from path on every attempt.
// The file is read again on every connection attempt, so a file that is missing or replaced
// is picked up without a restart. The contents are wiped after each read and are not logged.
func credentials(path string, logger *slog.Logger) nats.Option {
	fail := func(err error) error {
		logger.Warn("nats credentials could not be read, retrying", "path", path, "error", errorText(err))
		return fmt.Errorf("%w: %w", errCredentials, err)
	}
	return func(o *nats.Options) error {
		o.UserJWT = func() (string, error) {
			contents, err := os.ReadFile(path) // #nosec G304 -- paths are configured, not taken from a request
			if err != nil {
				return "", fail(err)
			}
			defer wipe(contents)
			token, err := nkeys.ParseDecoratedJWT(contents)
			if err != nil {
				return "", fail(err)
			}
			return token, nil
		}
		o.SignatureCB = func(nonce []byte) ([]byte, error) {
			contents, err := os.ReadFile(path) // #nosec G304 -- paths are configured, not taken from a request
			if err != nil {
				return nil, fail(err)
			}
			defer wipe(contents)
			kp, err := nkeys.ParseDecoratedNKey(contents)
			if err != nil {
				return nil, fail(err)
			}
			defer kp.Wipe()
			return kp.Sign(nonce)
		}
		return nil
	}
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// trustedCA trusts the certificates in path for a TLS server.
// The file is read on every TLS attempt, so a CA file that is missing at startup is picked up once it appears.
// A plaintext server URL does not install this option and does not read the file.
func trustedCA(path string, logger *slog.Logger) nats.Option {
	return func(o *nats.Options) error {
		if o.TLSConfig == nil {
			o.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		o.Secure = true
		o.RootCAsCB = func() (*x509.CertPool, error) {
			pemBytes, err := os.ReadFile(path) // #nosec G304 -- paths are configured, not taken from a request
			if err == nil {
				pool := x509.NewCertPool()
				if pool.AppendCertsFromPEM(pemBytes) {
					return pool, nil
				}
				err = errors.New("file holds no PEM certificate")
			}
			logger.Warn("nats CA file could not be read, retrying", "path", path, "error", errorText(err))
			return nil, fmt.Errorf("%w: CA file: %w", errCredentials, err)
		}
		return nil
	}
}

// secureURL reports whether any server in raw uses the tls scheme.
func secureURL(raw string) bool {
	for _, part := range strings.Split(raw, ",") {
		u, err := url.Parse(strings.TrimSpace(part))
		if err == nil && strings.EqualFold(u.Scheme, "tls") {
			return true
		}
	}
	return false
}

// redactURL drops any user name, token, or password from each server in raw.
func redactURL(raw string) string {
	parts := strings.Split(raw, ",")
	for i, part := range parts {
		u, err := url.Parse(strings.TrimSpace(part))
		if err != nil || u.Host == "" {
			parts[i] = "unparsed server address"
			continue
		}
		u.User = nil
		parts[i] = u.String()
	}
	return strings.Join(parts, ",")
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
