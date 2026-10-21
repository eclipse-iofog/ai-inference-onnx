package natsapi

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// clientName is how this process names its connection to the NATS server.
const clientName = "inference"

// ListenerConfig is how the listener reaches the NATS server.
type ListenerConfig struct {
	// URL is the server. nats:// is plaintext and tls:// is TLS. A comma separates more than one server.
	URL string
	// CredsPath is the user credentials file. It is read again on every connection attempt,
	// so a file that is missing or replaced is picked up without a restart.
	CredsPath string
	// CAFile is the authority trusted for a tls:// server. It is read again on every TLS attempt.
	// Empty uses the system trust store. A nats:// URL never reads it.
	CAFile string
	// Handler answers the requests.
	Handler *Handler
	// Logger records connect, disconnect, and retries. A nil logger records nothing.
	// The log does not include the credentials, the CA file contents, or a password in the URL.
	Logger *slog.Logger

	// reconnectWait and reconnectJitter replace the client's pause between attempts. Zero keeps the client default.
	reconnectWait   time.Duration
	reconnectJitter time.Duration
}

// Listener is the connection that serves the inference subjects.
// It dials in the background and keeps retrying for as long as the process runs.
// A server that cannot be reached, missing credentials, or a missing CA file do not stop the process.
type Listener struct {
	conn    *nats.Conn
	subs    []*nats.Subscription
	handler *Handler
}

// errCredentials marks a failed attempt that was already logged when the credentials or the CA file could not be read.
var errCredentials = errors.New("nats credentials")

// Dial connects to the server in the background and subscribes the handler on every request subject.
// It returns before the first connection is made. A failed attempt is retried without a limit,
// and the subscriptions, including the queue group, are restored on every reconnect.
// An error means the settings cannot be used. It is not a connection failure.
func Dial(cfg ListenerConfig) (*Listener, error) {
	if cfg.Handler == nil {
		return nil, errors.New("nats listener has no handler")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	opts := []nats.Option{
		nats.Name(clientName),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		// A rejected credential is retried like any other failure, so a rotated file can be picked up.
		nats.IgnoreAuthErrorAbort(),
		credentials(cfg.CredsPath, logger),
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
	if secureURL(cfg.URL) && cfg.CAFile != "" {
		opts = append(opts, trustedCA(cfg.CAFile, logger))
	}
	if cfg.reconnectWait > 0 {
		opts = append(opts, nats.ReconnectWait(cfg.reconnectWait))
	}
	if cfg.reconnectJitter > 0 {
		opts = append(opts, nats.ReconnectJitter(cfg.reconnectJitter, cfg.reconnectJitter))
	}
	conn, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats settings: %w", err)
	}
	subs, err := Subscribe(conn, cfg.Handler)
	if err != nil {
		conn.Close()
		return nil, err
	}
	logger.Info("nats listener is up", "server", redactURL(cfg.URL))
	return &Listener{conn: conn, subs: subs, handler: cfg.Handler}, nil
}

// Connected reports whether the connection to the server is up now.
// It is false for a nil listener, while the first dial is failing, and while the connection is down.
func (l *Listener) Connected() bool {
	return l != nil && l.conn.IsConnected()
}

// Close removes the subscriptions, waits for requests already received to be answered, and closes the connection.
// Cancel the handler's context first so a request waiting on a queue or an upload does not hold it back.
func (l *Listener) Close() {
	if l == nil {
		return
	}
	for _, sub := range l.subs {
		_ = sub.Unsubscribe()
	}
	l.handler.Wait()
	l.conn.Close()
}

// credentials reads the user JWT and signs the server nonce from path on every attempt.
// The client's own credentials option reads the file once while options are applied,
// which would turn a file that is missing at startup into a failed start instead of a retry.
// A file that cannot be read or parsed is logged as a retry. The contents are wiped after each read.
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

// trustedCA trusts the certificates in path for a TLS server. The file is read on every TLS attempt,
// so a CA file that is missing at startup is picked up once it appears.
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
