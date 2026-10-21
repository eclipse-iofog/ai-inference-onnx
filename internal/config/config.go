// Package config loads server settings and reads an optional per-model config file.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// AuthDisabled skips credential checks.
	AuthDisabled = "disabled"
	// AuthEdgelet requires an Edgelet service-account token.
	AuthEdgelet = "edgelet"
	// AuthStatic requires the configured bearer token.
	AuthStatic = "static"

	defaultModelsPath      = "/models"
	defaultKnowledgePath   = "/knowledge"
	defaultHTTPPort        = 8000
	defaultGRPCPort        = 8001
	defaultMetricsPort     = 8002
	defaultThreads         = 1
	defaultQueueCapacity   = 8
	defaultMaxPayloadBytes = 32 << 20 // 32 MiB
	defaultMetricsInterval = 10 * time.Second
	defaultConfigPath      = "/etc/inference/config.yaml"
	defaultLogLevel        = "info"
	minStaticTokenBytes    = 32
	serverIDBytes          = 16
)

// Config is the server settings after defaults, the YAML file, and the environment are applied.
type Config struct {
	// ModelsPath is the model repository root.
	ModelsPath string
	// KnowledgePath is the knowledge repository root.
	KnowledgePath string
	// HTTPPort is the REST listen port.
	HTTPPort int
	// GRPCPort is the gRPC listen port.
	GRPCPort int
	// MetricsPort is the metrics listen port.
	MetricsPort int
	// AuthMode is disabled, edgelet, or static.
	AuthMode string
	// AuthStaticToken is the bearer token used when AuthMode is static.
	AuthStaticToken string
	// NATSServerURL is the NATS server URL. Empty means unset.
	NATSServerURL string
	// NATSCredsPath is the NATS credentials file. Empty means unset.
	NATSCredsPath string
	// NATSCAFile is the authority trusted for a TLS NATS server. Empty means unset.
	NATSCAFile string
	// TLSEnabled is true when REST and gRPC present a certificate.
	TLSEnabled bool
	// TLSCert is the certificate presented by REST and gRPC. Empty means unset.
	TLSCert string
	// TLSKey is the private key for TLSCert. Empty means unset.
	TLSKey string
	// TLSCA is the authority client certificates must chain to. Empty means clients do not present one.
	TLSCA string
	// LogLevel is debug, info, warn, or error.
	LogLevel string
	// IntraOpThreads is the intra-op thread count.
	IntraOpThreads int
	// InterOpThreads is the inter-op thread count.
	InterOpThreads int
	// MaxPayloadBytes is the maximum request payload size.
	MaxPayloadBytes int
	// QueueCapacity is the per-model queue limit.
	QueueCapacity int
	// BatchDelayMicroseconds is how long a short queue waits before it runs.
	BatchDelayMicroseconds int
	// MetricsInterval is how often a metrics snapshot is published.
	MetricsInterval time.Duration
	// ServerID is the single token that identifies this process.
	ServerID string
	// MicroserviceName is the Edgelet microservice name when the platform set it.
	MicroserviceName string
	// NodeUID is the Edgelet node id when the platform set it.
	NodeUID string
	// ApplicationName is the Edgelet application name when the platform set it.
	ApplicationName string
}

// NATSEnabled reports whether a NATS server URL and a credentials path are both set.
func (cfg Config) NATSEnabled() bool {
	return cfg.NATSServerURL != "" && cfg.NATSCredsPath != ""
}

type fileConfig struct {
	ModelsPath             *string `yaml:"modelsPath"`
	KnowledgePath          *string `yaml:"knowledgePath"`
	HTTPPort               *int    `yaml:"httpPort"`
	GRPCPort               *int    `yaml:"grpcPort"`
	MetricsPort            *int    `yaml:"metricsPort"`
	AuthMode               *string `yaml:"authMode"`
	AuthStaticToken        *string `yaml:"authStaticToken"`
	NATSServerURL          *string `yaml:"natsServerUrl"`
	NATSCredsPath          *string `yaml:"natsCredsPath"`
	NATSCAFile             *string `yaml:"natsCAFile"`
	TLSEnabled             *bool   `yaml:"tlsEnabled"`
	TLSCert                *string `yaml:"tlsCert"`
	TLSKey                 *string `yaml:"tlsKey"`
	TLSCA                  *string `yaml:"tlsCA"`
	LogLevel               *string `yaml:"logLevel"`
	IntraOpThreads         *int    `yaml:"intraOpThreads"`
	InterOpThreads         *int    `yaml:"interOpThreads"`
	MaxPayloadBytes        *int    `yaml:"maxPayloadBytes"`
	QueueCapacity          *int    `yaml:"queueCapacity"`
	BatchDelayMicroseconds *int    `yaml:"batchDelayMicroseconds"`
	MetricsInterval        *string `yaml:"metricsInterval"`
	ServerID               *string `yaml:"serverId"`
}

// Load reads server settings. A missing file is not an error.
// CONFIG_PATH selects the file. When it is unset, the file is /etc/inference/config.yaml.
// The environment overrides the file, and the file overrides the built-in defaults.
func Load() (Config, error) {
	cfg := defaults()
	fileID, fileIDSet, err := applyFile(&cfg)
	if err != nil {
		return Config{}, err
	}
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	serverID, err := resolveServerID(fileID, fileIDSet)
	if err != nil {
		return Config{}, err
	}
	cfg.ServerID = serverID
	applyLabels(&cfg)
	if err := validate(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaults() Config {
	return Config{
		ModelsPath:      defaultModelsPath,
		KnowledgePath:   defaultKnowledgePath,
		HTTPPort:        defaultHTTPPort,
		GRPCPort:        defaultGRPCPort,
		MetricsPort:     defaultMetricsPort,
		IntraOpThreads:  defaultThreads,
		InterOpThreads:  defaultThreads,
		MaxPayloadBytes: defaultMaxPayloadBytes,
		QueueCapacity:   defaultQueueCapacity,
		MetricsInterval: defaultMetricsInterval,
		LogLevel:        defaultLogLevel,
	}
}

func configPath() string {
	raw, ok := os.LookupEnv("CONFIG_PATH")
	if !ok {
		return defaultConfigPath
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultConfigPath
	}
	return trimmed
}

func applyFile(cfg *Config) (string, bool, error) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read config file: %w", err)
	}
	var file fileConfig
	if err := yaml.Unmarshal(data, &file); err != nil {
		return "", false, fmt.Errorf("parse config file: %w", err)
	}
	setTrimmed(&cfg.ModelsPath, file.ModelsPath)
	setTrimmed(&cfg.KnowledgePath, file.KnowledgePath)
	setInt(&cfg.HTTPPort, file.HTTPPort)
	setInt(&cfg.GRPCPort, file.GRPCPort)
	setInt(&cfg.MetricsPort, file.MetricsPort)
	setTrimmed(&cfg.AuthMode, file.AuthMode)
	setRaw(&cfg.AuthStaticToken, file.AuthStaticToken)
	setTrimmed(&cfg.NATSServerURL, file.NATSServerURL)
	setTrimmed(&cfg.NATSCredsPath, file.NATSCredsPath)
	setNonEmpty(&cfg.NATSCAFile, file.NATSCAFile)
	if file.TLSEnabled != nil {
		cfg.TLSEnabled = *file.TLSEnabled
	}
	setTrimmed(&cfg.TLSCert, file.TLSCert)
	setTrimmed(&cfg.TLSKey, file.TLSKey)
	setTrimmed(&cfg.TLSCA, file.TLSCA)
	setNonEmpty(&cfg.LogLevel, file.LogLevel)
	setInt(&cfg.IntraOpThreads, file.IntraOpThreads)
	setInt(&cfg.InterOpThreads, file.InterOpThreads)
	setInt(&cfg.MaxPayloadBytes, file.MaxPayloadBytes)
	setInt(&cfg.QueueCapacity, file.QueueCapacity)
	setInt(&cfg.BatchDelayMicroseconds, file.BatchDelayMicroseconds)
	if file.MetricsInterval != nil {
		interval, parseErr := time.ParseDuration(strings.TrimSpace(*file.MetricsInterval))
		if parseErr != nil {
			return "", false, fmt.Errorf("metrics interval must be a duration: %w", parseErr)
		}
		cfg.MetricsInterval = interval
	}
	if file.ServerID == nil {
		return "", false, nil
	}
	serverID := strings.TrimSpace(*file.ServerID)
	if serverID == "" {
		return "", false, nil
	}
	return serverID, true, nil
}

func applyEnv(cfg *Config) error {
	applyTrimmedEnv(&cfg.ModelsPath, "MODELS_PATH")
	applyTrimmedEnv(&cfg.KnowledgePath, "KNOWLEDGE_PATH")
	if err := applyIntEnv(&cfg.HTTPPort, "HTTP_PORT"); err != nil {
		return err
	}
	if err := applyIntEnv(&cfg.GRPCPort, "GRPC_PORT"); err != nil {
		return err
	}
	if err := applyIntEnv(&cfg.MetricsPort, "METRICS_PORT"); err != nil {
		return err
	}
	if raw, ok := os.LookupEnv("AUTH_MODE"); ok {
		cfg.AuthMode = strings.TrimSpace(raw)
	}
	if raw, ok := os.LookupEnv("AUTH_STATIC_TOKEN"); ok {
		cfg.AuthStaticToken = raw
	}
	applyTrimmedEnv(&cfg.NATSServerURL, "NATS_SERVER_URL")
	applyTrimmedEnv(&cfg.NATSCredsPath, "NATS_CREDS_PATH")
	applyNonEmptyEnv(&cfg.NATSCAFile, "NATS_CA_FILE")
	if err := applyBoolEnv(&cfg.TLSEnabled, "TLS_ENABLED"); err != nil {
		return err
	}
	applyTrimmedEnv(&cfg.TLSCert, "TLS_CERT")
	applyTrimmedEnv(&cfg.TLSKey, "TLS_KEY")
	applyTrimmedEnv(&cfg.TLSCA, "TLS_CA")
	applyNonEmptyEnv(&cfg.LogLevel, "LOG_LEVEL")
	if err := applyIntEnv(&cfg.IntraOpThreads, "INTRA_OP_THREADS"); err != nil {
		return err
	}
	if err := applyIntEnv(&cfg.InterOpThreads, "INTER_OP_THREADS"); err != nil {
		return err
	}
	if err := applyIntEnv(&cfg.MaxPayloadBytes, "MAX_PAYLOAD_BYTES"); err != nil {
		return err
	}
	if err := applyIntEnv(&cfg.QueueCapacity, "QUEUE_CAPACITY"); err != nil {
		return err
	}
	if err := applyIntEnv(&cfg.BatchDelayMicroseconds, "BATCH_DELAY_US"); err != nil {
		return err
	}
	return applyDurationEnv(&cfg.MetricsInterval, "METRICS_INTERVAL")
}

func applyLabels(cfg *Config) {
	cfg.MicroserviceName = label("EDGELET_MICROSERVICE_NAME")
	cfg.NodeUID = label("EDGELET_NODE_UID")
	cfg.ApplicationName = label("EDGELET_APPLICATION_NAME")
}

func label(key string) string {
	value, ok := nonEmptyEnv(key)
	if !ok {
		return ""
	}
	return value
}

func validate(cfg *Config) error {
	if err := validateAuth(*cfg); err != nil {
		return err
	}
	if err := validateLogLevel(cfg); err != nil {
		return err
	}
	return validateTLS(*cfg)
}

func validateAuth(cfg Config) error {
	switch cfg.AuthMode {
	case AuthDisabled, AuthEdgelet, AuthStatic:
	case "":
		return errors.New("auth mode is required")
	default:
		return fmt.Errorf("auth mode %q is not supported", cfg.AuthMode)
	}
	if cfg.AuthMode != AuthStatic {
		return nil
	}
	if cfg.AuthStaticToken == "" {
		return errors.New("static token is required")
	}
	if len(cfg.AuthStaticToken) < minStaticTokenBytes {
		return errors.New("static token must be at least 32 bytes")
	}
	return nil
}

func validateLogLevel(cfg *Config) error {
	canonical := strings.ToLower(cfg.LogLevel)
	switch canonical {
	case "debug", "info", "warn", "error":
		cfg.LogLevel = canonical
		return nil
	default:
		return fmt.Errorf("log level %q is not supported", cfg.LogLevel)
	}
}

// validateTLS requires a readable certificate and key when TLS is on.
// A client CA, when set, must be readable too. When TLS is off, the three paths are not opened.
func validateTLS(cfg Config) error {
	if !cfg.TLSEnabled {
		return nil
	}
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

// resolveServerID picks the platform uid, then ENGINE_SERVER_ID, then the file, then a new id.
// An empty value after trim is unset. A chosen id that contains '.', '*', or '>' is rejected
// and the next source is not used.
func resolveServerID(fileID string, fileSet bool) (string, error) {
	if id, ok := nonEmptyEnv("EDGELET_MICROSERVICE_UID"); ok {
		return checkServerID(id)
	}
	if id, ok := nonEmptyEnv("ENGINE_SERVER_ID"); ok {
		return checkServerID(id)
	}
	if fileSet {
		return checkServerID(fileID)
	}
	return newServerID()
}

func checkServerID(id string) (string, error) {
	if strings.ContainsAny(id, ".*>") {
		return "", errors.New("server id must not contain '.', '*', or '>'")
	}
	return id, nil
}

func newServerID() (string, error) {
	var buf [serverIDBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate server id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

func nonEmptyEnv(key string) (string, bool) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

func applyTrimmedEnv(dst *string, key string) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return
	}
	*dst = strings.TrimSpace(raw)
}

// applyNonEmptyEnv stores a trimmed environment value. An empty value counts as unset.
func applyNonEmptyEnv(dst *string, key string) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return
	}
	*dst = trimmed
}

// applyBoolEnv reads true or false, ignoring case. An empty value is an error.
func applyBoolEnv(dst *bool, key string) error {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true":
		*dst = true
		return nil
	case "false":
		*dst = false
		return nil
	default:
		return fmt.Errorf("%s must be true or false", key)
	}
}

func applyIntEnv(dst *int, key string) error {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%s must be an integer: %w", key, err)
	}
	*dst = value
	return nil
}

func applyDurationEnv(dst *time.Duration, key string) error {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%s must be a duration: %w", key, err)
	}
	*dst = value
	return nil
}

func setTrimmed(dst *string, src *string) {
	if src == nil {
		return
	}
	*dst = strings.TrimSpace(*src)
}

// setNonEmpty stores a trimmed file value. An empty value counts as unset.
func setNonEmpty(dst *string, src *string) {
	if src == nil {
		return
	}
	trimmed := strings.TrimSpace(*src)
	if trimmed == "" {
		return
	}
	*dst = trimmed
}

func setRaw(dst *string, src *string) {
	if src == nil {
		return
	}
	*dst = *src
}

func setInt(dst *int, src *int) {
	if src == nil {
		return
	}
	*dst = *src
}
