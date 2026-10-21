package main

import (
	"strconv"
	"strings"
	"testing"
)

func TestSettings(t *testing.T) {
	t.Parallel()
	lookup := func(values map[string]string) envLookup {
		return func(key string) (string, bool) {
			value, ok := values[key]
			return value, ok
		}
	}

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()
		cfg, err := loadSettings(lookup(nil))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Port != 8080 || listenAddr(cfg.Port) != ":8080" {
			t.Fatalf("port %d addr %s", cfg.Port, listenAddr(cfg.Port))
		}
		if cfg.LogLevel != "info" || cfg.TLSEnabled || cfg.natsOn() {
			t.Fatalf("defaults %+v", cfg)
		}
		if cfg.TLSCert != "" || cfg.TLSKey != "" || cfg.TLSCA != "" {
			t.Fatalf("tls paths were read: %+v", cfg)
		}
	})

	t.Run("empty port and log level", func(t *testing.T) {
		t.Parallel()
		cfg, err := loadSettings(lookup(map[string]string{
			"PORT":      "  ",
			"LOG_LEVEL": "",
		}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Port != 8080 || cfg.LogLevel != "info" {
			t.Fatalf("%+v", cfg)
		}
	})

	t.Run("case", func(t *testing.T) {
		t.Parallel()
		_, err := loadSettings(lookup(map[string]string{
			"PORT":        "9090",
			"LOG_LEVEL":   "WARN",
			"TLS_ENABLED": "TRUE",
			"TLS_CERT":    "cert.pem",
			"TLS_KEY":     "key.pem",
		}))
		if err == nil {
			t.Fatal("missing certificate files were accepted")
		}
		cfg, err := loadSettings(lookup(map[string]string{
			"LOG_LEVEL":   "DeBuG",
			"TLS_ENABLED": "False",
			"TLS_CERT":    "missing",
		}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.LogLevel != "debug" || cfg.TLSEnabled || cfg.TLSCert != "" {
			t.Fatalf("%+v", cfg)
		}
	})

	t.Run("rejects", func(t *testing.T) {
		t.Parallel()
		cases := []map[string]string{
			{"PORT": "nope"},
			{"PORT": "0"},
			{"PORT": "65536"},
			{"PORT": "-1"},
			{"LOG_LEVEL": "trace"},
			{"TLS_ENABLED": ""},
			{"TLS_ENABLED": "yes"},
			{"TLS_ENABLED": " TRUE "},
		}
		for _, values := range cases {
			_, err := loadSettings(lookup(values))
			if err == nil {
				t.Fatalf("accepted %+v", values)
			}
		}
	})

	t.Run("nats empty is unset", func(t *testing.T) {
		t.Parallel()
		cfg, err := loadSettings(lookup(map[string]string{
			"NATS_SERVER_URL": "  ",
			"NATS_CREDS_PATH": "creds",
			"NATS_CA_FILE":    "",
		}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.natsOn() || cfg.NATSServerURL != "" || cfg.NATSCAFile != "" {
			t.Fatalf("%+v", cfg)
		}
		if cfg.NATSCredsPath != "creds" {
			t.Fatalf("creds %q", cfg.NATSCredsPath)
		}
	})

	t.Run("port bounds", func(t *testing.T) {
		t.Parallel()
		for _, port := range []int{1, 8080, 65535} {
			cfg, err := loadSettings(lookup(map[string]string{"PORT": strconv.Itoa(port)}))
			if err != nil || cfg.Port != port {
				t.Fatalf("port %d: %v %+v", port, err, cfg)
			}
		}
	})
}

func TestRedactURLDropsUserInfo(t *testing.T) {
	t.Parallel()
	got := redactURL("nats://user:secret@127.0.0.1:4222, not a url")
	if strings.Contains(got, "secret") || strings.Contains(got, "user") {
		t.Fatalf("redacted %s", got)
	}
	if !strings.Contains(got, "127.0.0.1:4222") || !strings.Contains(got, "unparsed") {
		t.Fatalf("redacted %s", got)
	}
}
