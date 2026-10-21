package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLoadDefaults checks built-in settings when the config file is missing.
func TestLoadDefaults(t *testing.T) {
	isolateEnv(t)
	t.Setenv("AUTH_MODE", AuthDisabled)
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))

	cfg := mustLoad(t)
	if cfg.ModelsPath != "/models" || cfg.KnowledgePath != "/knowledge" {
		t.Fatalf("paths %q %q", cfg.ModelsPath, cfg.KnowledgePath)
	}
	if cfg.HTTPPort != 8000 || cfg.GRPCPort != 8001 || cfg.MetricsPort != 8002 {
		t.Fatalf("ports %d %d %d", cfg.HTTPPort, cfg.GRPCPort, cfg.MetricsPort)
	}
	if cfg.IntraOpThreads != 1 || cfg.InterOpThreads != 1 {
		t.Fatalf("threads %d %d", cfg.IntraOpThreads, cfg.InterOpThreads)
	}
	if cfg.QueueCapacity != 8 || cfg.BatchDelayMicroseconds != 0 {
		t.Fatalf("queue %d delay %d", cfg.QueueCapacity, cfg.BatchDelayMicroseconds)
	}
	if cfg.MaxPayloadBytes != 32<<20 {
		t.Fatalf("payload %d", cfg.MaxPayloadBytes)
	}
	if cfg.MetricsInterval != 10*time.Second {
		t.Fatalf("interval %s", cfg.MetricsInterval)
	}
	if cfg.NATSServerURL != "" || cfg.NATSCredsPath != "" || cfg.NATSEnabled() {
		t.Fatalf("nats url %q creds %q enabled %t", cfg.NATSServerURL, cfg.NATSCredsPath, cfg.NATSEnabled())
	}
	if cfg.LogLevel != "info" || cfg.TLSEnabled {
		t.Fatalf("log %q tls %t", cfg.LogLevel, cfg.TLSEnabled)
	}
	if cfg.TLSCert != "" || cfg.TLSKey != "" || cfg.TLSCA != "" || cfg.NATSCAFile != "" {
		t.Fatalf("cert %q key %q ca %q nats ca %q", cfg.TLSCert, cfg.TLSKey, cfg.TLSCA, cfg.NATSCAFile)
	}
	if cfg.MicroserviceName != "" || cfg.NodeUID != "" || cfg.ApplicationName != "" {
		t.Fatalf("labels %q %q %q", cfg.MicroserviceName, cfg.NodeUID, cfg.ApplicationName)
	}
	assertRandomID(t, cfg.ServerID)
}

// TestLoadMissingModelDirectory checks that a missing model directory is left unread.
func TestLoadMissingModelDirectory(t *testing.T) {
	isolateEnv(t)
	t.Setenv("AUTH_MODE", AuthDisabled)
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
	missing := filepath.Join(t.TempDir(), "models-not-here")
	t.Setenv("MODELS_PATH", missing)

	cfg := mustLoad(t)
	if cfg.ModelsPath != missing {
		t.Fatalf("models path %q", cfg.ModelsPath)
	}
}

// TestLoadPrecedence checks that the file overrides defaults and the environment overrides the file.
func TestLoadPrecedence(t *testing.T) {
	t.Run("file overrides default and env overrides file", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		path := writeConfig(t, `
modelsPath: /from-file
httpPort: 9000
metricsInterval: 1m
queueCapacity: 4
`)
		t.Setenv("CONFIG_PATH", path)
		t.Setenv("HTTP_PORT", "9001")
		t.Setenv("QUEUE_CAPACITY", "5")

		cfg := mustLoad(t)
		if cfg.ModelsPath != "/from-file" {
			t.Fatalf("models path %q", cfg.ModelsPath)
		}
		if cfg.HTTPPort != 9001 {
			t.Fatalf("http port %d", cfg.HTTPPort)
		}
		if cfg.QueueCapacity != 5 {
			t.Fatalf("queue %d", cfg.QueueCapacity)
		}
		if cfg.MetricsInterval != time.Minute {
			t.Fatalf("interval %s", cfg.MetricsInterval)
		}
		if cfg.KnowledgePath != "/knowledge" {
			t.Fatalf("knowledge path %q", cfg.KnowledgePath)
		}
	})

	t.Run("config path selects the file", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		first := writeConfig(t, "modelsPath: /first\n")
		second := writeConfig(t, "modelsPath: /second\n")
		t.Setenv("CONFIG_PATH", second)
		if cfg := mustLoad(t); cfg.ModelsPath != "/second" {
			t.Fatalf("selected %q from %s, other file %s", cfg.ModelsPath, second, first)
		}
	})
}

// TestLoadAuthMode checks the accepted auth modes and the values that are rejected.
func TestLoadAuthMode(t *testing.T) {
	token := strings.Repeat("a", 32)
	cases := []struct {
		name    string
		env     string
		envSet  bool
		file    string
		token   string
		want    string
		wantErr bool
	}{
		{name: "disabled", env: AuthDisabled, envSet: true, want: AuthDisabled},
		{name: "edgelet", env: AuthEdgelet, envSet: true, want: AuthEdgelet},
		{name: "static", env: AuthStatic, envSet: true, token: token, want: AuthStatic},
		{name: "unset", wantErr: true},
		{name: "empty", env: "", envSet: true, wantErr: true},
		{name: "blank", env: "   ", envSet: true, wantErr: true},
		{name: "other", env: "nope", envSet: true, wantErr: true},
		{name: "from file", file: "authMode: edgelet\n", want: AuthEdgelet},
		{name: "env overrides file", env: AuthDisabled, envSet: true, file: "authMode: edgelet\n", want: AuthDisabled},
		{name: "trimmed", env: " static ", envSet: true, token: token, want: AuthStatic},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateEnv(t)
			if tc.file != "" {
				t.Setenv("CONFIG_PATH", writeConfig(t, tc.file))
			} else {
				t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
			}
			if tc.envSet {
				t.Setenv("AUTH_MODE", tc.env)
			}
			if tc.token != "" {
				t.Setenv("AUTH_STATIC_TOKEN", tc.token)
			}
			cfg, err := Load()
			if tc.wantErr {
				if err == nil {
					t.Fatal("load succeeded")
				}
				return
			}
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if cfg.AuthMode != tc.want {
				t.Fatalf("auth mode %q", cfg.AuthMode)
			}
		})
	}
}

// TestLoadStaticToken checks the static token length and that errors do not echo it.
func TestLoadStaticToken(t *testing.T) {
	short := "s3cr3t-value-should-stay-hidden"
	if len(short) != 31 {
		t.Fatalf("fixture is %d bytes", len(short))
	}
	long := short + "!"

	t.Run("missing", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthStatic)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		err := loadErr(t)
		if strings.Contains(err.Error(), short) {
			t.Fatalf("error contains token material: %v", err)
		}
	})

	t.Run("31 bytes", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthStatic)
		t.Setenv("AUTH_STATIC_TOKEN", short)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		err := loadErr(t)
		if strings.Contains(err.Error(), short) {
			t.Fatalf("error contains token: %v", err)
		}
	})

	t.Run("31 bytes in file", func(t *testing.T) {
		isolateEnv(t)
		body := "authMode: static\nauthStaticToken: " + short + "\n"
		t.Setenv("CONFIG_PATH", writeConfig(t, body))
		err := loadErr(t)
		if strings.Contains(err.Error(), short) {
			t.Fatalf("error contains token: %v", err)
		}
	})

	t.Run("32 bytes", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthStatic)
		t.Setenv("AUTH_STATIC_TOKEN", long)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		cfg := mustLoad(t)
		if cfg.AuthStaticToken != long {
			t.Fatal("token was not loaded")
		}
	})

	t.Run("env overrides file", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthStatic)
		t.Setenv("CONFIG_PATH", writeConfig(t, "authStaticToken: "+strings.Repeat("b", 32)+"\n"))
		t.Setenv("AUTH_STATIC_TOKEN", long)
		cfg := mustLoad(t)
		if cfg.AuthStaticToken != long {
			t.Fatal("env token did not override the file")
		}
	})
}

// TestLoadDisabledDoesNotRequireToken checks that disabled and edgelet do not require a token.
func TestLoadDisabledDoesNotRequireToken(t *testing.T) {
	short := strings.Repeat("q", 31)

	t.Run("missing", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		cfg := mustLoad(t)
		if cfg.AuthMode != AuthDisabled || cfg.AuthStaticToken != "" {
			t.Fatalf("mode %q token set %t", cfg.AuthMode, cfg.AuthStaticToken != "")
		}
	})

	t.Run("short token", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("AUTH_STATIC_TOKEN", short)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		if cfg := mustLoad(t); cfg.AuthMode != AuthDisabled {
			t.Fatalf("mode %q", cfg.AuthMode)
		}
	})

	t.Run("edgelet ignores token", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthEdgelet)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		if cfg := mustLoad(t); cfg.AuthMode != AuthEdgelet {
			t.Fatalf("mode %q", cfg.AuthMode)
		}
	})
}

// TestLoadInvalidYAML checks that a file that is not valid YAML is rejected.
func TestLoadInvalidYAML(t *testing.T) {
	isolateEnv(t)
	t.Setenv("AUTH_MODE", AuthDisabled)
	t.Setenv("CONFIG_PATH", writeConfig(t, "httpPort: [\n"))
	mustFail(t)
}

// TestLoadUnknownKey checks that an unknown key leaves a known default in place.
func TestLoadUnknownKey(t *testing.T) {
	isolateEnv(t)
	t.Setenv("AUTH_MODE", AuthDisabled)
	t.Setenv("CONFIG_PATH", writeConfig(t, "notARealKey: 99\nhttp_port: 9999\n"))
	cfg := mustLoad(t)
	if cfg.HTTPPort != 8000 || cfg.ModelsPath != "/models" {
		t.Fatalf("port %d path %q", cfg.HTTPPort, cfg.ModelsPath)
	}
}

// TestLoadBadNumbers checks that a non-integer port and a bad interval are rejected.
func TestLoadBadNumbers(t *testing.T) {
	t.Run("port", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		t.Setenv("HTTP_PORT", "abc")
		mustFail(t)
	})

	t.Run("port in file", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "grpcPort: nope\n"))
		mustFail(t)
	})

	t.Run("interval", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		t.Setenv("METRICS_INTERVAL", "tomorrow")
		mustFail(t)
	})

	t.Run("interval in file", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "metricsInterval: nope\n"))
		mustFail(t)
	})
}

// TestLoadZeroValues checks that an explicit zero is stored instead of the default.
func TestLoadZeroValues(t *testing.T) {
	isolateEnv(t)
	t.Setenv("AUTH_MODE", AuthDisabled)
	t.Setenv("CONFIG_PATH", writeConfig(t, `
intraOpThreads: 0
interOpThreads: 0
queueCapacity: 0
batchDelayMicroseconds: 0
maxPayloadBytes: 0
httpPort: 9
metricsInterval: 0s
`))
	t.Setenv("HTTP_PORT", "0")

	cfg := mustLoad(t)
	if cfg.HTTPPort != 0 || cfg.IntraOpThreads != 0 || cfg.InterOpThreads != 0 {
		t.Fatalf("ports and threads %d %d %d", cfg.HTTPPort, cfg.IntraOpThreads, cfg.InterOpThreads)
	}
	if cfg.QueueCapacity != 0 || cfg.BatchDelayMicroseconds != 0 || cfg.MaxPayloadBytes != 0 {
		t.Fatalf("queue %d delay %d payload %d", cfg.QueueCapacity, cfg.BatchDelayMicroseconds, cfg.MaxPayloadBytes)
	}
	if cfg.MetricsInterval != 0 {
		t.Fatalf("interval %s", cfg.MetricsInterval)
	}
	if cfg.GRPCPort != 8001 {
		t.Fatalf("grpc port %d", cfg.GRPCPort)
	}
}

// TestLoadServerID checks source order, generated ids, and rejected subject tokens.
func TestLoadServerID(t *testing.T) {
	t.Run("platform wins", func(t *testing.T) {
		isolateEnv(t)
		prepareServerID(t, "serverId: from-file\n")
		t.Setenv("EDGELET_MICROSERVICE_UID", "platform")
		t.Setenv("ENGINE_SERVER_ID", "from-env")
		first := mustLoad(t)
		second := mustLoad(t)
		if first.ServerID != "platform" || second.ServerID != "platform" {
			t.Fatalf("ids %q %q", first.ServerID, second.ServerID)
		}
	})

	t.Run("env wins over file", func(t *testing.T) {
		isolateEnv(t)
		prepareServerID(t, "serverId: from-file\n")
		t.Setenv("ENGINE_SERVER_ID", "from-env")
		if cfg := mustLoad(t); cfg.ServerID != "from-env" {
			t.Fatalf("server id %q", cfg.ServerID)
		}
	})

	t.Run("file wins when env is unset", func(t *testing.T) {
		isolateEnv(t)
		prepareServerID(t, "serverId: from-file\n")
		if cfg := mustLoad(t); cfg.ServerID != "from-file" {
			t.Fatalf("server id %q", cfg.ServerID)
		}
	})

	t.Run("blank platform falls through", func(t *testing.T) {
		isolateEnv(t)
		prepareServerID(t, "serverId: from-file\n")
		t.Setenv("EDGELET_MICROSERVICE_UID", "   ")
		t.Setenv("ENGINE_SERVER_ID", "from-env")
		if cfg := mustLoad(t); cfg.ServerID != "from-env" {
			t.Fatalf("server id %q", cfg.ServerID)
		}
	})

	t.Run("blank env falls through", func(t *testing.T) {
		isolateEnv(t)
		prepareServerID(t, "serverId: from-file\n")
		t.Setenv("ENGINE_SERVER_ID", " \t ")
		if cfg := mustLoad(t); cfg.ServerID != "from-file" {
			t.Fatalf("server id %q", cfg.ServerID)
		}
	})

	t.Run("trimmed", func(t *testing.T) {
		isolateEnv(t)
		prepareServerID(t, "serverId: from-file\n")
		t.Setenv("EDGELET_MICROSERVICE_UID", "  platform  ")
		if cfg := mustLoad(t); cfg.ServerID != "platform" {
			t.Fatalf("server id %q", cfg.ServerID)
		}
	})

	t.Run("generated ids differ", func(t *testing.T) {
		isolateEnv(t)
		prepareServerID(t, "")
		first := mustLoad(t)
		second := mustLoad(t)
		assertRandomID(t, first.ServerID)
		assertRandomID(t, second.ServerID)
		if first.ServerID == second.ServerID {
			t.Fatalf("two loads returned %q", first.ServerID)
		}
	})

	t.Run("rejected platform does not use env", func(t *testing.T) {
		for _, id := range []string{"bad.id", "bad*id", "bad>id"} {
			t.Run(id, func(t *testing.T) {
				isolateEnv(t)
				prepareServerID(t, "serverId: from-file\n")
				t.Setenv("EDGELET_MICROSERVICE_UID", id)
				t.Setenv("ENGINE_SERVER_ID", "from-env")
				mustFail(t)
			})
		}
	})

	t.Run("rejected env does not use file", func(t *testing.T) {
		isolateEnv(t)
		prepareServerID(t, "serverId: from-file\n")
		t.Setenv("ENGINE_SERVER_ID", "bad.id")
		mustFail(t)
	})

	t.Run("rejected file does not generate", func(t *testing.T) {
		isolateEnv(t)
		prepareServerID(t, "serverId: bad*id\n")
		mustFail(t)
	})

	t.Run("later rejected id is ignored", func(t *testing.T) {
		isolateEnv(t)
		prepareServerID(t, "serverId: bad>id\n")
		t.Setenv("EDGELET_MICROSERVICE_UID", "platform")
		t.Setenv("ENGINE_SERVER_ID", "bad.id")
		if cfg := mustLoad(t); cfg.ServerID != "platform" {
			t.Fatalf("server id %q", cfg.ServerID)
		}
	})
}

// TestLoadNATS checks that NATS stays off unless both values are set, and that empty is unset.
func TestLoadNATS(t *testing.T) {
	t.Run("both unset", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		cfg := mustLoad(t)
		if cfg.NATSEnabled() || cfg.NATSServerURL != "" || cfg.NATSCredsPath != "" {
			t.Fatalf("url %q creds %q enabled %t", cfg.NATSServerURL, cfg.NATSCredsPath, cfg.NATSEnabled())
		}
	})

	t.Run("both set", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "natsServerUrl: nats://leaf:4222\nnatsCredsPath: /creds\n"))
		cfg := mustLoad(t)
		if !cfg.NATSEnabled() || cfg.NATSServerURL != "nats://leaf:4222" || cfg.NATSCredsPath != "/creds" {
			t.Fatalf("url %q creds %q enabled %t", cfg.NATSServerURL, cfg.NATSCredsPath, cfg.NATSEnabled())
		}
	})

	t.Run("empty url is unset", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "natsServerUrl: nats://leaf:4222\nnatsCredsPath: /creds\n"))
		t.Setenv("NATS_SERVER_URL", "")
		cfg := mustLoad(t)
		if cfg.NATSServerURL != "" || cfg.NATSCredsPath != "/creds" || cfg.NATSEnabled() {
			t.Fatalf("url %q creds %q enabled %t", cfg.NATSServerURL, cfg.NATSCredsPath, cfg.NATSEnabled())
		}
	})

	t.Run("empty creds are unset", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "natsServerUrl: nats://leaf:4222\nnatsCredsPath: /creds\n"))
		t.Setenv("NATS_CREDS_PATH", "  ")
		cfg := mustLoad(t)
		if cfg.NATSCredsPath != "" || cfg.NATSEnabled() {
			t.Fatalf("creds %q enabled %t", cfg.NATSCredsPath, cfg.NATSEnabled())
		}
	})

	t.Run("empty file value is unset", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "natsServerUrl: \"\"\nnatsCredsPath: \"\"\n"))
		cfg := mustLoad(t)
		if cfg.NATSServerURL != "" || cfg.NATSCredsPath != "" || cfg.NATSEnabled() {
			t.Fatalf("url %q creds %q enabled %t", cfg.NATSServerURL, cfg.NATSCredsPath, cfg.NATSEnabled())
		}
	})
}

// TestLoadSecurityPrecedence checks that the file overrides defaults and the environment overrides the file.
func TestLoadSecurityPrecedence(t *testing.T) {
	cert := writeFileMode(t, "cert", 0o600)
	key := writeFileMode(t, "key", 0o600)
	ca := writeFileMode(t, "ca", 0o600)
	natsCA := filepath.Join(t.TempDir(), "nats-ca.pem")

	t.Run("file overrides default", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		body := fmt.Sprintf("logLevel: debug\ntlsEnabled: true\ntlsCert: %q\ntlsKey: %q\ntlsCA: %q\nnatsCAFile: %q\n", cert, key, ca, natsCA)
		t.Setenv("CONFIG_PATH", writeConfig(t, body))

		cfg := mustLoad(t)
		if cfg.LogLevel != "debug" || !cfg.TLSEnabled {
			t.Fatalf("log %q tls %t", cfg.LogLevel, cfg.TLSEnabled)
		}
		if cfg.TLSCert != cert || cfg.TLSKey != key || cfg.TLSCA != ca || cfg.NATSCAFile != natsCA {
			t.Fatalf("cert %q key %q ca %q nats ca %q", cfg.TLSCert, cfg.TLSKey, cfg.TLSCA, cfg.NATSCAFile)
		}
		if cfg.HTTPPort != 8000 || cfg.ModelsPath != "/models" || cfg.QueueCapacity != 8 {
			t.Fatalf("port %d path %q queue %d", cfg.HTTPPort, cfg.ModelsPath, cfg.QueueCapacity)
		}
	})

	t.Run("env overrides file", func(t *testing.T) {
		envCert := writeFileMode(t, "env-cert", 0o600)
		envKey := writeFileMode(t, "env-key", 0o600)
		envCA := writeFileMode(t, "env-ca", 0o600)
		envNATS := filepath.Join(t.TempDir(), "env-nats-ca.pem")
		blocked := writeFileMode(t, "blocked", 0o000)
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		body := fmt.Sprintf("logLevel: trace\ntlsEnabled: true\ntlsCert: %q\ntlsKey: %q\ntlsCA: %q\nnatsCAFile: /from-file\nmodelsPath: /from-file\n", blocked, blocked, blocked)
		t.Setenv("CONFIG_PATH", writeConfig(t, body))
		t.Setenv("LOG_LEVEL", "debug")
		t.Setenv("TLS_ENABLED", "true")
		t.Setenv("TLS_CERT", envCert)
		t.Setenv("TLS_KEY", envKey)
		t.Setenv("TLS_CA", envCA)
		t.Setenv("NATS_CA_FILE", envNATS)

		cfg := mustLoad(t)
		if cfg.LogLevel != "debug" || !cfg.TLSEnabled {
			t.Fatalf("log %q tls %t", cfg.LogLevel, cfg.TLSEnabled)
		}
		if cfg.TLSCert != envCert || cfg.TLSKey != envKey || cfg.TLSCA != envCA || cfg.NATSCAFile != envNATS {
			t.Fatalf("cert %q key %q ca %q nats ca %q", cfg.TLSCert, cfg.TLSKey, cfg.TLSCA, cfg.NATSCAFile)
		}
		if cfg.ModelsPath != "/from-file" || cfg.HTTPPort != 8000 {
			t.Fatalf("path %q port %d", cfg.ModelsPath, cfg.HTTPPort)
		}
	})
}

// TestLoadLogLevel checks accepted levels and that an empty value stays unset.
func TestLoadLogLevel(t *testing.T) {
	t.Run("empty env keeps file", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "logLevel: debug\n"))
		t.Setenv("LOG_LEVEL", "")
		if cfg := mustLoad(t); cfg.LogLevel != "debug" {
			t.Fatalf("log %q", cfg.LogLevel)
		}
	})

	t.Run("blank env keeps file", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "logLevel: debug\n"))
		t.Setenv("LOG_LEVEL", " \t ")
		if cfg := mustLoad(t); cfg.LogLevel != "debug" {
			t.Fatalf("log %q", cfg.LogLevel)
		}
	})

	t.Run("both empty are info", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "logLevel: \"\"\n"))
		t.Setenv("LOG_LEVEL", "")
		if cfg := mustLoad(t); cfg.LogLevel != "info" {
			t.Fatalf("log %q", cfg.LogLevel)
		}
	})

	t.Run("blank file is info", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "logLevel: \"   \"\n"))
		if cfg := mustLoad(t); cfg.LogLevel != "info" {
			t.Fatalf("log %q", cfg.LogLevel)
		}
	})

	t.Run("WARN", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		t.Setenv("LOG_LEVEL", "WARN")
		if cfg := mustLoad(t); cfg.LogLevel != "warn" {
			t.Fatalf("log %q", cfg.LogLevel)
		}
	})

	t.Run("debug", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "logLevel: DEBUG\n"))
		if cfg := mustLoad(t); cfg.LogLevel != "debug" {
			t.Fatalf("log %q", cfg.LogLevel)
		}
	})

	t.Run("trace", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		t.Setenv("LOG_LEVEL", "trace")
		mustFail(t)
	})

	t.Run("trace in file", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "logLevel: trace\n"))
		mustFail(t)
	})

	t.Run("other", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		t.Setenv("LOG_LEVEL", "verbose")
		mustFail(t)
	})
}

// TestLoadTLSEnabled checks the accepted flag values and the ones that are rejected.
func TestLoadTLSEnabled(t *testing.T) {
	cert := writeFileMode(t, "cert", 0o600)
	key := writeFileMode(t, "key", 0o600)

	t.Run("true", func(t *testing.T) {
		isolateEnv(t)
		prepareTLS(t, cert, key, "")
		t.Setenv("TLS_ENABLED", "true")
		if cfg := mustLoad(t); !cfg.TLSEnabled {
			t.Fatal("tls disabled")
		}
	})

	t.Run("TRUE", func(t *testing.T) {
		isolateEnv(t)
		prepareTLS(t, cert, key, "")
		t.Setenv("TLS_ENABLED", "TRUE")
		cfg := mustLoad(t)
		if !cfg.TLSEnabled || cfg.TLSCert != cert || cfg.TLSKey != key || cfg.TLSCA != "" {
			t.Fatalf("tls %t cert %q key %q ca %q", cfg.TLSEnabled, cfg.TLSCert, cfg.TLSKey, cfg.TLSCA)
		}
	})

	t.Run("false", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "tlsEnabled: true\n"))
		t.Setenv("TLS_ENABLED", "false")
		t.Setenv("TLS_CERT", filepath.Join(t.TempDir(), "missing-cert"))
		if cfg := mustLoad(t); cfg.TLSEnabled {
			t.Fatal("tls enabled")
		}
	})

	t.Run("False", func(t *testing.T) {
		blocked := writeFileMode(t, "blocked", 0o000)
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		body := fmt.Sprintf("tlsEnabled: TRUE\ntlsCert: %q\ntlsKey: %q\ntlsCA: %q\n", blocked, blocked, blocked)
		t.Setenv("CONFIG_PATH", writeConfig(t, body))
		t.Setenv("TLS_ENABLED", "False")
		cfg := mustLoad(t)
		if cfg.TLSEnabled || cfg.TLSCert != blocked || cfg.TLSKey != blocked || cfg.TLSCA != blocked {
			t.Fatalf("tls %t cert %q key %q ca %q", cfg.TLSEnabled, cfg.TLSCert, cfg.TLSKey, cfg.TLSCA)
		}
	})

	for _, value := range []string{"yes", "1", "", "   "} {
		name := value
		if value == "" {
			name = "empty"
		} else if strings.TrimSpace(value) == "" {
			name = "blank"
		}
		t.Run(name, func(t *testing.T) {
			isolateEnv(t)
			t.Setenv("AUTH_MODE", AuthDisabled)
			t.Setenv("CONFIG_PATH", writeConfig(t, "tlsEnabled: false\n"))
			t.Setenv("TLS_ENABLED", value)
			mustFail(t)
		})
	}
}

// TestLoadTLSFiles checks the certificate pair and that TLS off does not open those paths.
func TestLoadTLSFiles(t *testing.T) {
	t.Run("pair and ca", func(t *testing.T) {
		cert := writeFileMode(t, "cert", 0o600)
		key := writeFileMode(t, "key", 0o600)
		ca := writeFileMode(t, "ca", 0o600)
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		body := fmt.Sprintf("tlsEnabled: TRUE\ntlsCert: %q\ntlsKey: %q\ntlsCA: %q\n", cert, key, ca)
		t.Setenv("CONFIG_PATH", writeConfig(t, body))
		cfg := mustLoad(t)
		if !cfg.TLSEnabled || cfg.TLSCert != cert || cfg.TLSKey != key || cfg.TLSCA != ca {
			t.Fatalf("tls %t cert %q key %q ca %q", cfg.TLSEnabled, cfg.TLSCert, cfg.TLSKey, cfg.TLSCA)
		}
	})

	t.Run("empty ca is unset", func(t *testing.T) {
		cert := writeFileMode(t, "cert", 0o600)
		key := writeFileMode(t, "key", 0o600)
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		body := fmt.Sprintf("tlsEnabled: true\ntlsCert: %q\ntlsKey: %q\ntlsCA: \"   \"\n", cert, key)
		t.Setenv("CONFIG_PATH", writeConfig(t, body))
		cfg := mustLoad(t)
		if !cfg.TLSEnabled || cfg.TLSCA != "" {
			t.Fatalf("tls %t ca %q", cfg.TLSEnabled, cfg.TLSCA)
		}
	})

	t.Run("empty ca env clears file", func(t *testing.T) {
		cert := writeFileMode(t, "cert", 0o600)
		key := writeFileMode(t, "key", 0o600)
		ca := writeFileMode(t, "ca", 0o600)
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		body := fmt.Sprintf("tlsEnabled: true\ntlsCert: %q\ntlsKey: %q\ntlsCA: %q\n", cert, key, ca)
		t.Setenv("CONFIG_PATH", writeConfig(t, body))
		t.Setenv("TLS_CA", "")
		cfg := mustLoad(t)
		if !cfg.TLSEnabled || cfg.TLSCA != "" || cfg.TLSCert != cert {
			t.Fatalf("tls %t cert %q ca %q", cfg.TLSEnabled, cfg.TLSCert, cfg.TLSCA)
		}
	})

	t.Run("only certificate", func(t *testing.T) {
		isolateEnv(t)
		prepareTLS(t, writeFileMode(t, "cert", 0o600), "", "")
		t.Setenv("TLS_ENABLED", "true")
		mustFail(t)
	})

	t.Run("only key", func(t *testing.T) {
		isolateEnv(t)
		prepareTLS(t, "", writeFileMode(t, "key", 0o600), "")
		t.Setenv("TLS_ENABLED", "true")
		mustFail(t)
	})

	t.Run("neither path", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "tlsEnabled: true\n"))
		mustFail(t)
	})

	t.Run("unreadable certificate", func(t *testing.T) {
		isolateEnv(t)
		prepareTLS(t, writeFileMode(t, "cert", 0o000), writeFileMode(t, "key", 0o600), "")
		t.Setenv("TLS_ENABLED", "true")
		mustFail(t)
	})

	t.Run("unreadable key", func(t *testing.T) {
		isolateEnv(t)
		prepareTLS(t, writeFileMode(t, "cert", 0o600), writeFileMode(t, "key", 0o000), "")
		t.Setenv("TLS_ENABLED", "true")
		mustFail(t)
	})

	t.Run("missing certificate", func(t *testing.T) {
		isolateEnv(t)
		prepareTLS(t, filepath.Join(t.TempDir(), "missing-cert"), writeFileMode(t, "key", 0o600), "")
		t.Setenv("TLS_ENABLED", "true")
		mustFail(t)
	})

	t.Run("unreadable ca", func(t *testing.T) {
		isolateEnv(t)
		prepareTLS(t, writeFileMode(t, "cert", 0o600), writeFileMode(t, "key", 0o600), writeFileMode(t, "ca", 0o000))
		t.Setenv("TLS_ENABLED", "true")
		mustFail(t)
	})

	t.Run("missing ca", func(t *testing.T) {
		isolateEnv(t)
		prepareTLS(t, writeFileMode(t, "cert", 0o600), writeFileMode(t, "key", 0o600), filepath.Join(t.TempDir(), "missing-ca"))
		t.Setenv("TLS_ENABLED", "true")
		mustFail(t)
	})

	t.Run("disabled leaves missing paths", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		body := fmt.Sprintf("tlsEnabled: FALSE\ntlsCert: %q\ntlsKey: %q\ntlsCA: %q\n", missing, missing+"-key", missing+"-ca")
		t.Setenv("CONFIG_PATH", writeConfig(t, body))
		cfg := mustLoad(t)
		if cfg.TLSEnabled || cfg.TLSCert != missing || cfg.TLSKey != missing+"-key" || cfg.TLSCA != missing+"-ca" {
			t.Fatalf("tls %t cert %q key %q ca %q", cfg.TLSEnabled, cfg.TLSCert, cfg.TLSKey, cfg.TLSCA)
		}
	})

	t.Run("disabled leaves unreadable paths", func(t *testing.T) {
		blocked := writeFileMode(t, "blocked", 0o000)
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		t.Setenv("TLS_CERT", blocked)
		t.Setenv("TLS_KEY", blocked)
		t.Setenv("TLS_CA", blocked)
		cfg := mustLoad(t)
		if cfg.TLSEnabled || cfg.TLSCert != blocked || cfg.TLSKey != blocked || cfg.TLSCA != blocked {
			t.Fatalf("tls %t cert %q key %q ca %q", cfg.TLSEnabled, cfg.TLSCert, cfg.TLSKey, cfg.TLSCA)
		}
	})

	t.Run("default leaves one unreadable path", func(t *testing.T) {
		blocked := writeFileMode(t, "blocked", 0o000)
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		t.Setenv("TLS_KEY", blocked)
		cfg := mustLoad(t)
		if cfg.TLSEnabled || cfg.LogLevel != "info" || cfg.TLSKey != blocked || cfg.TLSCert != "" || cfg.TLSCA != "" {
			t.Fatalf("tls %t log %q key %q", cfg.TLSEnabled, cfg.LogLevel, cfg.TLSKey)
		}
	})
}

// TestLoadNATSCAFile checks that the CA path is stored and is not opened.
func TestLoadNATSCAFile(t *testing.T) {
	t.Run("missing path is stored", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "no-such-ca.pem")
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		t.Setenv("NATS_CA_FILE", missing)
		cfg := mustLoad(t)
		if cfg.NATSCAFile != missing || cfg.NATSEnabled() {
			t.Fatalf("ca %q enabled %t", cfg.NATSCAFile, cfg.NATSEnabled())
		}
	})

	t.Run("unreadable path is stored", func(t *testing.T) {
		blocked := writeFileMode(t, "ca", 0o000)
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, fmt.Sprintf("natsCAFile: %q\n", blocked)))
		if cfg := mustLoad(t); cfg.NATSCAFile != blocked {
			t.Fatalf("ca %q", cfg.NATSCAFile)
		}
	})

	t.Run("empty env keeps file", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "natsCAFile: /from-file\n"))
		t.Setenv("NATS_CA_FILE", "")
		if cfg := mustLoad(t); cfg.NATSCAFile != "/from-file" {
			t.Fatalf("ca %q", cfg.NATSCAFile)
		}
	})

	t.Run("blank env keeps file", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "natsCAFile: /from-file\n"))
		t.Setenv("NATS_CA_FILE", "  ")
		if cfg := mustLoad(t); cfg.NATSCAFile != "/from-file" {
			t.Fatalf("ca %q", cfg.NATSCAFile)
		}
	})

	t.Run("both empty are unset", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "natsCAFile: \"\"\n"))
		t.Setenv("NATS_CA_FILE", "")
		if cfg := mustLoad(t); cfg.NATSCAFile != "" {
			t.Fatalf("ca %q", cfg.NATSCAFile)
		}
	})

	t.Run("blank file is unset", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AUTH_MODE", AuthDisabled)
		t.Setenv("CONFIG_PATH", writeConfig(t, "natsCAFile: \"   \"\n"))
		if cfg := mustLoad(t); cfg.NATSCAFile != "" {
			t.Fatalf("ca %q", cfg.NATSCAFile)
		}
	})
}

// TestLoadIgnoresFlags checks that process arguments are not a settings source.
func TestLoadIgnoresFlags(t *testing.T) {
	isolateEnv(t)
	t.Setenv("AUTH_MODE", AuthDisabled)
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
	args := os.Args
	os.Args = []string{"inference", "-logLevel", "trace", "-tlsEnabled=true"}
	t.Cleanup(func() { os.Args = args })

	cfg := mustLoad(t)
	if cfg.LogLevel != "info" || cfg.TLSEnabled || cfg.HTTPPort != 8000 {
		t.Fatalf("log %q tls %t port %d", cfg.LogLevel, cfg.TLSEnabled, cfg.HTTPPort)
	}
}

// TestLoadEdgeletLabels checks that the platform label variables are stored when set.
func TestLoadEdgeletLabels(t *testing.T) {
	isolateEnv(t)
	t.Setenv("AUTH_MODE", AuthDisabled)
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
	t.Setenv("EDGELET_MICROSERVICE_NAME", "engine")
	t.Setenv("EDGELET_NODE_UID", "node-1")
	t.Setenv("EDGELET_APPLICATION_NAME", "catalog")

	cfg := mustLoad(t)
	if cfg.MicroserviceName != "engine" || cfg.NodeUID != "node-1" || cfg.ApplicationName != "catalog" {
		t.Fatalf("labels %q %q %q", cfg.MicroserviceName, cfg.NodeUID, cfg.ApplicationName)
	}
}

func isolateEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"CONFIG_PATH",
		"MODELS_PATH",
		"KNOWLEDGE_PATH",
		"HTTP_PORT",
		"GRPC_PORT",
		"METRICS_PORT",
		"AUTH_MODE",
		"AUTH_STATIC_TOKEN",
		"NATS_SERVER_URL",
		"NATS_CREDS_PATH",
		"NATS_CA_FILE",
		"TLS_ENABLED",
		"TLS_CERT",
		"TLS_KEY",
		"TLS_CA",
		"LOG_LEVEL",
		"INTRA_OP_THREADS",
		"INTER_OP_THREADS",
		"MAX_PAYLOAD_BYTES",
		"QUEUE_CAPACITY",
		"BATCH_DELAY_US",
		"METRICS_INTERVAL",
		"ENGINE_SERVER_ID",
		"EDGELET_MICROSERVICE_UID",
		"EDGELET_MICROSERVICE_NAME",
		"EDGELET_NODE_UID",
		"EDGELET_APPLICATION_NAME",
	}
	for _, key := range keys {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}
}

func prepareTLS(t *testing.T, cert, key, ca string) {
	t.Helper()
	t.Setenv("AUTH_MODE", AuthDisabled)
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
	if cert != "" {
		t.Setenv("TLS_CERT", cert)
	}
	if key != "" {
		t.Setenv("TLS_KEY", key)
	}
	if ca != "" {
		t.Setenv("TLS_CA", ca)
	}
}

func prepareServerID(t *testing.T, body string) {
	t.Helper()
	t.Setenv("AUTH_MODE", AuthDisabled)
	if body == "" {
		t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		return
	}
	t.Setenv("CONFIG_PATH", writeConfig(t, body))
}

func writeFileMode(t *testing.T, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", name, err)
	}
	if mode&0o400 == 0 {
		t.Cleanup(func() {
			_ = os.Chmod(path, 0o600)
		})
	}
	return path
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func mustLoad(t *testing.T) Config {
	t.Helper()
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

func mustFail(t *testing.T) {
	t.Helper()
	if _, err := Load(); err == nil {
		t.Fatal("load succeeded")
	}
}

func loadErr(t *testing.T) error {
	t.Helper()
	_, err := Load()
	if err == nil {
		t.Fatal("load succeeded")
	}
	return err
}

func assertRandomID(t *testing.T, id string) {
	t.Helper()
	raw, err := hex.DecodeString(id)
	if err != nil {
		t.Fatalf("server id %q: %v", id, err)
	}
	if len(raw) != 16 || hex.EncodeToString(raw) != id {
		t.Fatalf("server id %q", id)
	}
}
