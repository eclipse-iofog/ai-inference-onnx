package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
)

// loadListenerTLS loads the certificate both listeners present.
// It returns nil when TLS is off, and the listeners stay plain HTTP and cleartext HTTP/2.
// A client authority, when set, requires a client certificate that chains to it.
// The files are read before either listener accepts a connection.
func loadListenerTLS(cfg config.Config) (*tls.Config, error) {
	if !cfg.TLSEnabled {
		return nil, nil
	}
	pair, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
	if err != nil {
		return nil, fmt.Errorf("tls certificate: %w", err)
	}
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{pair},
		MinVersion:   tls.VersionTLS12,
	}
	if cfg.TLSCA == "" {
		return tlsConfig, nil
	}
	pemBytes, err := os.ReadFile(cfg.TLSCA)
	if err != nil {
		return nil, fmt.Errorf("tls ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, errors.New("tls ca is not a certificate")
	}
	tlsConfig.ClientCAs = pool
	tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	return tlsConfig, nil
}
