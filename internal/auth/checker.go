// Package auth decides whether a REST or gRPC call is allowed.
// Health and metrics stay open. Other calls are allowed, unauthenticated, or forbidden.
package auth

import (
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
)

const (
	// PublicJWKPath is the Edgelet public signing key inside the container.
	PublicJWKPath = "/var/run/secrets/edgelet.iofog.org/serviceaccount/edgelet.jwk"

	edgeletIssuer          = "https://edgelet.default.svc.bridge.local"
	tokenUseServiceAccount = "serviceaccount"
	apiGroup               = "inference.iofog.org/v2"
)

// Decision is the result of an authentication check.
type Decision int

const (
	// Allow means the call may proceed.
	Allow Decision = iota
	// Unauthenticated means the credential is missing or not valid.
	Unauthenticated
	// Forbidden means the credential is valid and does not allow the call.
	Forbidden
)

// ErrUnauthenticated is returned when the credential is missing or not valid.
var ErrUnauthenticated = errors.New("unauthenticated")

// ErrForbidden is returned when the credential is valid and does not allow the call.
var ErrForbidden = errors.New("forbidden")

// String returns the decision name.
func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Unauthenticated:
		return "unauthenticated"
	case Forbidden:
		return "forbidden"
	default:
		return "unknown"
	}
}

// Checker decides REST and gRPC calls from the loaded server settings.
// The static token is not reloaded. In edgelet mode the public key is.
type Checker struct {
	mode        string
	staticToken string
	keys        *keyFile
}

// New prepares a checker from server settings.
// jwkPath is the public signing key file. An empty path uses PublicJWKPath.
// In edgelet mode a missing or unusable key file returns an error.
func New(cfg config.Config, jwkPath string) (*Checker, error) {
	switch cfg.AuthMode {
	case config.AuthDisabled:
		return &Checker{mode: cfg.AuthMode}, nil
	case config.AuthStatic:
		if cfg.AuthStaticToken == "" {
			return nil, errors.New("static token is required")
		}
		return &Checker{mode: cfg.AuthMode, staticToken: cfg.AuthStaticToken}, nil
	case config.AuthEdgelet:
		keys, err := openKeyFile(resolveKeyPath(jwkPath))
		if err != nil {
			return nil, err
		}
		return &Checker{mode: cfg.AuthMode, keys: keys}, nil
	default:
		return nil, errors.New("auth mode is not supported")
	}
}

// AuthorizeHTTP decides a REST call.
// method and path identify the operation. authorization is the Authorization header.
// now is the time used for expiry, not-before, and issued-at checks.
func (c *Checker) AuthorizeHTTP(method, path, authorization string, now time.Time) (Decision, error) {
	return c.decide(operationFromHTTP(method, path), authorization, now)
}

// AuthorizeGRPC decides a gRPC call.
// fullMethod is the RPC name, such as /inference.GRPCInferenceService/ModelInfer.
// model is the model name when the RPC has one.
// version is the model version. An empty version uses the same resource as a versioned call.
// authorization is the authorization metadata value.
// now is the time used for expiry, not-before, and issued-at checks.
func (c *Checker) AuthorizeGRPC(fullMethod, model, version, authorization string, now time.Time) (Decision, error) {
	// An empty version and a set version use the same resource.
	return c.decide(operationFromGRPC(fullMethod, model), authorization, now)
}

func (c *Checker) decide(op operation, authorization string, now time.Time) (Decision, error) {
	if op.exempt || c.mode == config.AuthDisabled {
		return Allow, nil
	}
	switch c.mode {
	case config.AuthStatic:
		if !staticMatch(authorization, c.staticToken) {
			return Unauthenticated, ErrUnauthenticated
		}
		return Allow, nil
	case config.AuthEdgelet:
		return c.decideEdgelet(op, authorization, now)
	default:
		return Unauthenticated, ErrUnauthenticated
	}
}

func (c *Checker) decideEdgelet(op operation, authorization string, now time.Time) (Decision, error) {
	token, ok := bearerToken(authorization)
	if !ok {
		return Unauthenticated, ErrUnauthenticated
	}
	if c.keys == nil {
		return Unauthenticated, ErrUnauthenticated
	}
	key, ok := c.keys.current()
	if !ok {
		return Unauthenticated, ErrUnauthenticated
	}
	claims, ok := verifiedClaims(token, key)
	if !ok {
		return Unauthenticated, ErrUnauthenticated
	}
	if !claimsAcceptable(claims, now) {
		return Unauthenticated, ErrUnauthenticated
	}
	if !op.known || !rulesAllow(claims, op.resource, op.verb) {
		return Forbidden, ErrForbidden
	}
	return Allow, nil
}

func staticMatch(header, want string) bool {
	got, ok := bearerToken(header)
	if !ok || want == "" || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func bearerToken(header string) (string, bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || fields[1] == "" {
		return "", false
	}
	return fields[1], true
}
