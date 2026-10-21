package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// loadListenerTLS loads the certificate the listener presents.
// It returns nil when TLS is off, and the listener stays plain HTTP.
// A client authority, when set, requires a client certificate that chains to it.
// The files are read before the listener accepts a connection.
// When TLS is off, the certificate, key, and client authority are not opened.
func loadListenerTLS(cfg config) (*tls.Config, error) {
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
	pemBytes, err := os.ReadFile(cfg.TLSCA) // #nosec G304 -- paths are configured, not taken from a request
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
