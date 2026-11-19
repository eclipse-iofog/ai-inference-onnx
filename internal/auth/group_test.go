package auth

import (
	"net/http"
	"strings"
	"testing"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
)

// TestEdgeletChecksOneGroup checks that a token is allowed only for the group selected at startup.
func TestEdgeletChecksOneGroup(t *testing.T) {
	public, private := newSigningKey(t)
	path := keyFilePath(t)
	writePublicJWK(t, path, public)
	now := checkTime()
	rule := accessRule([]string{"server"}, []string{"get"})
	cases := []struct {
		name  string
		cfg   config.Config
		group string
		other string
	}{
		{
			name:  "prefix",
			cfg:   config.Config{AuthMode: config.AuthEdgelet, APIGroupPrefix: "plant"},
			group: "plant." + config.DefaultAPIGroup,
			other: config.DefaultAPIGroup,
		},
		{
			name: "platform names",
			cfg: config.Config{
				AuthMode:         config.AuthEdgelet,
				ApplicationName:  "inference",
				MicroserviceName: "engine",
			},
			group: "inference.engine." + config.DefaultAPIGroup,
			other: config.DefaultAPIGroup,
		},
		{
			name: "prefix wins over names",
			cfg: config.Config{
				AuthMode:         config.AuthEdgelet,
				APIGroupPrefix:   "plant",
				ApplicationName:  "inference",
				MicroserviceName: "engine",
			},
			group: "plant." + config.DefaultAPIGroup,
			other: "inference.engine." + config.DefaultAPIGroup,
		},
		{
			name: "one platform name",
			cfg: config.Config{
				AuthMode:         config.AuthEdgelet,
				MicroserviceName: "Engine",
			},
			group: config.DefaultAPIGroup,
			other: "Engine." + config.DefaultAPIGroup,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checker, err := New(tc.cfg, path)
			if err != nil {
				t.Fatalf("checker: %v", err)
			}
			allowed := signClaims(t, private, claimsFor(now, tc.group, rule))
			decision, gotErr := checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(allowed), now)
			assertDecision(t, decision, gotErr, Allow)
			rejected := signClaims(t, private, claimsFor(now, tc.other, rule))
			decision, gotErr = checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(rejected), now)
			assertDecision(t, decision, gotErr, Forbidden)
		})
	}
}

// TestAPIGroupPrefixRejected checks that a prefix that is not DNS labels is rejected in every auth mode.
func TestAPIGroupPrefixRejected(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	for _, mode := range []string{config.AuthDisabled, config.AuthStatic, config.AuthEdgelet} {
		t.Run(mode, func(t *testing.T) {
			_, err := New(config.Config{
				AuthMode:        mode,
				AuthStaticToken: token,
				APIGroupPrefix:  "Plant",
			}, keyFilePath(t))
			if err == nil || !strings.Contains(err.Error(), "Plant") {
				t.Fatalf("error %v", err)
			}
			assertNoSecret(t, err, token)
		})
	}
}

// TestAPIGroupNamesRejected checks that unusable platform names stop edgelet mode and leave other modes on the default group.
func TestAPIGroupNamesRejected(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	cfg := config.Config{
		ApplicationName:  "Line",
		MicroserviceName: "engine",
	}
	path := keyFilePath(t)
	now := checkTime()

	t.Run("edgelet", func(t *testing.T) {
		cfg.AuthMode = config.AuthEdgelet
		_, err := New(cfg, path)
		if err == nil || !strings.Contains(err.Error(), "Line") {
			t.Fatalf("error %v", err)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		cfg.AuthMode = config.AuthDisabled
		checker, err := New(cfg, path)
		if err != nil {
			t.Fatalf("checker: %v", err)
		}
		if checker.apiGroup != config.DefaultAPIGroup {
			t.Fatalf("group %q", checker.apiGroup)
		}
	})

	t.Run("static", func(t *testing.T) {
		cfg.AuthMode = config.AuthStatic
		cfg.AuthStaticToken = token
		checker, err := New(cfg, path)
		if err != nil {
			t.Fatalf("checker: %v", err)
		}
		if checker.apiGroup != config.DefaultAPIGroup {
			t.Fatalf("group %q", checker.apiGroup)
		}
		decision, gotErr := checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(token), now)
		assertDecision(t, decision, gotErr, Allow)
		assertNoSecret(t, gotErr, token)
	})
}
