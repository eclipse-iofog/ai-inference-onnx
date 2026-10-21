package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nkeys"
)

// natsBox is an embedded server and the credentials file the engine reads.
type natsBox struct {
	srv          *server.Server
	credsPath    string
	clientURL    string
	containerURL string
	dir          string
}

func startNATS() (*natsBox, error) {
	opts := &server.Options{
		Host:   "0.0.0.0",
		Port:   server.RANDOM_PORT,
		NoLog:  true,
		NoSigs: true,
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		return nil, err
	}
	srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		srv.Shutdown()
		return nil, errors.New("embedded NATS server did not start")
	}
	addr, ok := srv.Addr().(*net.TCPAddr)
	if !ok {
		srv.Shutdown()
		return nil, fmt.Errorf("NATS address %T", srv.Addr())
	}
	dir, err := os.MkdirTemp("", "engine-nats-")
	if err != nil {
		srv.Shutdown()
		return nil, err
	}
	if err := os.Chmod(dir, 0o755); err != nil { // #nosec G302 -- the container user is not the host user and must traverse this directory
		srv.Shutdown()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	creds := filepath.Join(dir, "user.creds")
	contents, err := mintCredentials()
	if err != nil {
		srv.Shutdown()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := os.WriteFile(creds, contents, 0o644); err != nil { // #nosec G306 -- the container user reads this bind-mounted credentials file
		srv.Shutdown()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &natsBox{
		srv:          srv,
		credsPath:    creds,
		clientURL:    fmt.Sprintf("nats://127.0.0.1:%d", addr.Port),
		containerURL: fmt.Sprintf("nats://host.docker.internal:%d", addr.Port),
		dir:          dir,
	}, nil
}

func (n *natsBox) close() {
	if n == nil {
		return
	}
	if n.srv != nil {
		n.srv.Shutdown()
	}
	if n.dir != "" {
		_ = os.RemoveAll(n.dir)
	}
}

// mintCredentials writes a user JWT and seed in the credentials file format.
// The server does not require them. The engine reads the file on each dial.
func mintCredentials() ([]byte, error) {
	account, err := nkeys.CreateAccount()
	if err != nil {
		return nil, err
	}
	user, err := nkeys.CreateUser()
	if err != nil {
		return nil, err
	}
	userPub, err := user.PublicKey()
	if err != nil {
		return nil, err
	}
	userJWT, err := jwt.NewUserClaims(userPub).Encode(account)
	if err != nil {
		return nil, err
	}
	seed, err := user.Seed()
	if err != nil {
		return nil, err
	}
	return jwt.FormatUserConfig(userJWT, seed)
}
