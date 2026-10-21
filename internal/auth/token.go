package auth

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func verifiedClaims(token string, key ed25519.PublicKey) (jwt.MapClaims, bool) {
	parser := jwt.NewParser(
		jwt.WithoutClaimsValidation(),
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
	)
	parsed, err := parser.Parse(token, func(parsed *jwt.Token) (any, error) {
		if parsed.Method == nil || parsed.Method.Alg() != jwt.SigningMethodEdDSA.Alg() {
			return nil, errUnexpectedAlgorithm
		}
		return key, nil
	})
	if err != nil || parsed == nil || !parsed.Valid {
		return nil, false
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, false
	}
	return claims, true
}

var errUnexpectedAlgorithm = errors.New("unexpected signing method")

func claimsAcceptable(claims jwt.MapClaims, now time.Time) bool {
	use, ok := claims["tokenUse"].(string)
	if !ok || use != tokenUseServiceAccount {
		return false
	}
	issuer, ok := claims["iss"].(string)
	if !ok || issuer != edgeletIssuer {
		return false
	}
	issuedAt, ok := unixSeconds(claims["iat"])
	if !ok || time.Unix(issuedAt, 0).After(now) {
		return false
	}
	notBefore, ok := unixSeconds(claims["nbf"])
	if !ok || time.Unix(notBefore, 0).After(now) {
		return false
	}
	expires, ok := unixSeconds(claims["exp"])
	if !ok || !time.Unix(expires, 0).After(now) {
		return false
	}
	return true
}

func unixSeconds(raw any) (int64, bool) {
	switch value := raw.(type) {
	case float64:
		if value < 0 || value != math.Trunc(value) {
			return 0, false
		}
		return int64(value), true
	case int64:
		return value, true
	case int:
		return int64(value), true
	case json.Number:
		seconds, err := value.Int64()
		if err != nil {
			return 0, false
		}
		return seconds, true
	default:
		return 0, false
	}
}

func rulesAllow(claims jwt.MapClaims, resource, verb string) bool {
	groups, ok := rulesByGroup(claims)
	if !ok {
		return false
	}
	raw, ok := groups[apiGroup]
	if !ok {
		return false
	}
	rules, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, item := range rules {
		rule, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if verbAllowed(rule["verbs"], verb) && resourceAllowed(rule["resources"], resource) {
			return true
		}
	}
	return false
}

func rulesByGroup(claims jwt.MapClaims) (map[string]any, bool) {
	top, ok := claims["edgelet.iofog.org"].(map[string]any)
	if !ok {
		return nil, false
	}
	rbac, ok := top["rbac"].(map[string]any)
	if !ok {
		return nil, false
	}
	groups, ok := rbac["rulesByGroup"].(map[string]any)
	if !ok {
		return nil, false
	}
	return groups, true
}

func verbAllowed(raw any, verb string) bool {
	values, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			continue
		}
		if strings.ToLower(strings.TrimSpace(text)) == verb {
			return true
		}
	}
	return false
}

func resourceAllowed(raw any, resource string) bool {
	values, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, value := range values {
		pattern, ok := value.(string)
		if !ok {
			continue
		}
		if resourceMatch(pattern, resource) {
			return true
		}
	}
	return false
}

func resourceMatch(pattern, resource string) bool {
	if pattern == resource {
		return true
	}
	left := strings.Split(pattern, "/")
	right := strings.Split(resource, "/")
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] == "*" {
			if right[i] == "" {
				return false
			}
			continue
		}
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
