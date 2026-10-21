package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// container is one engine process started from the image.
type container struct {
	name    string
	id      string
	http    int
	grpc    int
	metrics int
}

type engineSpec struct {
	models  string
	creds   string
	natsURL string
	publish bool
	libPath string
}

// runFlags are applied to every container: a read-only root, a tmpfs at /tmp,
// no capabilities, and no new privileges.
func runFlags() []string {
	return []string{
		"--read-only",
		"--tmpfs", "/tmp",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
	}
}

func startEngine(name, image string, spec engineSpec) (*container, error) {
	removeContainer(name)
	ctr := &container{name: name}
	args := []string{"run", "--rm", "-d", "--name", name}
	args = append(args, runFlags()...)
	args = append(args, "--add-host=host.docker.internal:host-gateway")
	env := []string{"AUTH_MODE=disabled"}
	if spec.natsURL != "" {
		env = append(env, "NATS_SERVER_URL="+spec.natsURL)
	}
	if spec.creds != "" {
		env = append(env, "NATS_CREDS_PATH=/home/inference/nats.creds")
		args = append(args, "-v", spec.creds+":/home/inference/nats.creds:ro")
	}
	if spec.libPath != "" {
		env = append(env, "ORT_LIB_PATH="+spec.libPath)
	}
	if spec.models != "" {
		args = append(args, "-v", spec.models+":/models")
	}
	if spec.publish {
		var err error
		ctr.http, err = freePort()
		if err != nil {
			return nil, err
		}
		ctr.grpc, err = freePort()
		if err != nil {
			return nil, err
		}
		ctr.metrics, err = freePort()
		if err != nil {
			return nil, err
		}
		args = append(args,
			"-p", "127.0.0.1:"+strconv.Itoa(ctr.http)+":8000",
			"-p", "127.0.0.1:"+strconv.Itoa(ctr.grpc)+":8001",
			"-p", "127.0.0.1:"+strconv.Itoa(ctr.metrics)+":8002",
		)
	}
	for _, item := range env {
		args = append(args, "-e", item)
	}
	args = append(args, image)
	out, err := dockerCmd(args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("start %s: %w\n%s", name, err, out)
	}
	ctr.id = strings.TrimSpace(string(out))
	return ctr, nil
}

func (c *container) remove() {
	if c == nil || c.name == "" {
		return
	}
	removeContainer(c.name)
}

func removeContainer(name string) {
	_ = dockerCmd("rm", "-f", name).Run()
}

func (c *container) running() bool {
	if c == nil || c.id == "" {
		return false
	}
	out, err := dockerCmd("inspect", "-f", "{{.State.Running}}", c.id).Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func (c *container) logs() string {
	if c == nil || c.id == "" {
		return ""
	}
	out, err := dockerCmd("logs", c.id).CombinedOutput()
	if err != nil && len(out) == 0 {
		return err.Error()
	}
	return string(out)
}

func (c *container) fail(err error) error {
	if err == nil {
		return nil
	}
	text := c.logs()
	const tail = 8000
	if len(text) > tail {
		text = text[len(text)-tail:]
	}
	return fmt.Errorf("%w\ncontainer log:\n%s", err, text)
}

func (c *container) base() string {
	return "http://127.0.0.1:" + strconv.Itoa(c.http)
}

func (c *container) metricsURL() string {
	return "http://127.0.0.1:" + strconv.Itoa(c.metrics)
}

func (c *container) waitReady(timeout time.Duration) error {
	if err := waitHTTP(c.base()+"/v2/health/live", 200, timeout); err != nil {
		return fmt.Errorf("live: %w", err)
	}
	if err := waitHTTP(c.base()+"/v2/health/ready", 200, timeout); err != nil {
		return fmt.Errorf("ready: %w", err)
	}
	return nil
}

func (c *container) waitLive(timeout time.Duration) error {
	if err := waitHTTP(c.base()+"/v2/health/live", 200, timeout); err != nil {
		return fmt.Errorf("live: %w", err)
	}
	if !c.running() {
		return errors.New("container exited")
	}
	return nil
}

func inspectImage(image string) error {
	out, err := dockerCmd("inspect", "--format", "{{json .Config}}", image).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("docker inspect: %s", exit.Stderr)
		}
		return fmt.Errorf("docker inspect: %w", err)
	}
	var cfg struct {
		User       string   `json:"User"`
		Env        []string `json:"Env"`
		Entrypoint []string `json:"Entrypoint"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		return fmt.Errorf("docker inspect: %w", err)
	}
	if cfg.User != "10001:10001" {
		return fmt.Errorf("image user %q", cfg.User)
	}
	if len(cfg.Entrypoint) != 1 || cfg.Entrypoint[0] != "/usr/local/bin/inference" {
		return fmt.Errorf("image entrypoint %q", cfg.Entrypoint)
	}
	want := "ORT_LIB_PATH=" + libPath
	for _, item := range cfg.Env {
		if item == want {
			return nil
		}
	}
	return fmt.Errorf("image environment has no %s", want)
}

// shellEntrypointRuns starts /bin/sh in the image. A UBI image includes that shell.
func shellEntrypointRuns(image string) error {
	args := []string{"run", "--rm", "--name", "engine-image-test-shell"}
	args = append(args, runFlags()...)
	args = append(args, "--entrypoint", "/bin/sh", image, "-c", "exit 0")
	removeContainer("engine-image-test-shell")
	out, err := dockerCmd(args...).CombinedOutput()
	removeContainer("engine-image-test-shell")
	text := string(out)
	if strings.Contains(text, "Cannot connect to the Docker daemon") || strings.Contains(text, "error during connect") {
		return fmt.Errorf("docker: %s", text)
	}
	if err != nil {
		return fmt.Errorf("shell entrypoint failed:\n%s", text)
	}
	return nil
}

// dockerCmd runs the docker client. The arguments are chosen by this program.
func dockerCmd(args ...string) *exec.Cmd {
	return exec.Command("docker", args...) // #nosec G204 -- arguments are built by this program, not taken from a request
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		return 0, err
	}
	return port, nil
}

func closedPort() (int, error) {
	port, err := freePort()
	if err != nil {
		return 0, err
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return 0, fmt.Errorf("port %d accepted a connection", port)
	}
	return port, nil
}
