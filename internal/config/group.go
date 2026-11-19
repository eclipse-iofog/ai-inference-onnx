package config

import (
	"fmt"
	"strings"
)

const (
	// DefaultAPIGroup is the rulesByGroup key when no prefix is set and the platform did not provide both names.
	DefaultAPIGroup = "inference.iofog.org/v2"

	apiGroupName = "inference.iofog.org"
	maxDNSName   = 253
	maxDNSLabel  = 63
)

// ResolveAPIGroup returns the rulesByGroup key for cfg.
// A set prefix is used first. Both platform names are used when the prefix is empty.
// Otherwise the key is DefaultAPIGroup.
func (cfg Config) ResolveAPIGroup() (string, error) {
	prefix := strings.TrimSpace(cfg.APIGroupPrefix)
	if prefix != "" {
		if !dnsPrefix(prefix) {
			return "", fmt.Errorf("api group prefix %q must be DNS labels", prefix)
		}
		return prefix + "." + DefaultAPIGroup, nil
	}
	application := strings.TrimSpace(cfg.ApplicationName)
	microservice := strings.TrimSpace(cfg.MicroserviceName)
	if application == "" || microservice == "" {
		return DefaultAPIGroup, nil
	}
	derived := application + "." + microservice
	if !dnsPrefix(derived) {
		if cfg.AuthMode == AuthEdgelet {
			return "", fmt.Errorf("application name %q and microservice name %q must be DNS labels, or set an api group prefix", application, microservice)
		}
		return DefaultAPIGroup, nil
	}
	return derived + "." + DefaultAPIGroup, nil
}

func dnsPrefix(prefix string) bool {
	name := prefix + "." + apiGroupName
	if prefix == "" || len(name) > maxDNSName {
		return false
	}
	for _, label := range strings.Split(prefix, ".") {
		if !dnsLabel(label) {
			return false
		}
	}
	return true
}

func dnsLabel(label string) bool {
	n := len(label)
	if n == 0 || n > maxDNSLabel {
		return false
	}
	for i := 0; i < n; i++ {
		c := label[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			continue
		}
		if c == '-' && i > 0 && i < n-1 {
			continue
		}
		return false
	}
	return true
}
