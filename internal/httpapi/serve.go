package httpapi

import (
	"crypto/tls"
	"net"
	"net/http"
)

// Serve accepts connections on ln and runs server.
// A nil tlsConfig serves plain HTTP. Otherwise the listener presents the certificate in tlsConfig.
func Serve(server *http.Server, ln net.Listener, tlsConfig *tls.Config) error {
	if tlsConfig == nil {
		return server.Serve(ln)
	}
	server.TLSConfig = tlsConfig.Clone()
	return server.ServeTLS(ln, "", "")
}
