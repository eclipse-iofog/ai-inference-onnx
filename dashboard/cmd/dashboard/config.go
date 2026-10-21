package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	defaultPort     = 8080
	defaultLogLevel = "info"
)

// envLookup reads one environment variable.
// The boolean is false when the variable is unset.
type envLookup func(string) (string, bool)

// config is the listener's own settings.
// They come from the environment. The engine configuration file is not read.
type config struct {
	Port          int
	LogLevel      string
	TLSEnabled    bool
	TLSCert       string
	TLSKey        string
	TLSCA         string
	NATSServerURL string
	NATSCredsPath string
	NATSCAFile    string
}

// natsOn reports whether both the server URL and the credentials path are set.
func (cfg config) natsOn() bool {
	return cfg.NATSServerURL != "" && cfg.NATSCredsPath != ""
}

func loadConfig() (config, error) {
	return loadSettings(os.LookupEnv)
}

func loadSettings(lookup envLookup) (config, error) {
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	cfg := config{
		Port:     defaultPort,
		LogLevel: defaultLogLevel,
	}
	port, err := parsePort(lookup)
	if err != nil {
		return config{}, err
	}
	cfg.Port = port
	level, err := parseLogLevel(lookup)
	if err != nil {
		return config{}, err
	}
	cfg.LogLevel = level
	enabled, err := parseTLSEnabled(lookup)
	if err != nil {
		return config{}, err
	}
	cfg.TLSEnabled = enabled
	if enabled {
		cfg.TLSCert = nonEmpty(lookup, "TLS_CERT")
		cfg.TLSKey = nonEmpty(lookup, "TLS_KEY")
		cfg.TLSCA = nonEmpty(lookup, "TLS_CA")
		if err := validateTLS(cfg); err != nil {
			return config{}, err
		}
	}
	cfg.NATSServerURL = nonEmpty(lookup, "NATS_SERVER_URL")
	cfg.NATSCredsPath = nonEmpty(lookup, "NATS_CREDS_PATH")
	cfg.NATSCAFile = nonEmpty(lookup, "NATS_CA_FILE")
	return cfg, nil
}

func parsePort(lookup envLookup) (int, error) {
	raw, ok := lookup("PORT")
	if !ok {
		return defaultPort, nil
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultPort, nil
	}
	port, err := strconv.Atoi(trimmed)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("port %q is not a valid port", trimmed)
	}
	return port, nil
}

func parseLogLevel(lookup envLookup) (string, error) {
	raw, ok := lookup("LOG_LEVEL")
	if !ok {
		return defaultLogLevel, nil
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultLogLevel, nil
	}
	level := strings.ToLower(trimmed)
	switch level {
	case "debug", "info", "warn", "error":
		return level, nil
	default:
		return "", fmt.Errorf("log level %q is not supported", trimmed)
	}
}

func parseTLSEnabled(lookup envLookup) (bool, error) {
	raw, ok := lookup("TLS_ENABLED")
	if !ok {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("TLS_ENABLED must be true or false")
	}
}

// validateTLS requires a readable certificate and key when TLS is on.
// A client authority, when set, must be readable too.
// The caller does not invoke this when TLS is off, so those paths are not opened.
func validateTLS(cfg config) error {
	if cfg.TLSCert == "" || cfg.TLSKey == "" {
		return errors.New("tls certificate and key are required")
	}
	if err := readableFile(cfg.TLSCert); err != nil {
		return fmt.Errorf("open tls certificate: %w", err)
	}
	if err := readableFile(cfg.TLSKey); err != nil {
		return fmt.Errorf("open tls key: %w", err)
	}
	if cfg.TLSCA == "" {
		return nil
	}
	if err := readableFile(cfg.TLSCA); err != nil {
		return fmt.Errorf("open tls ca: %w", err)
	}
	return nil
}

func readableFile(path string) error {
	file, err := os.Open(path) // #nosec G304 -- paths are configured, not taken from a request
	if err != nil {
		return err
	}
	return file.Close()
}

func nonEmpty(lookup envLookup, key string) string {
	raw, ok := lookup(key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(raw)
}
