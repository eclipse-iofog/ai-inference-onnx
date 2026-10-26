package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type modelCopy struct {
	root string
	file string
	onnx []byte
}

func copyAddModel(root string) (modelCopy, error) {
	src := filepath.Join(root, "testdata", "models", "add", "model.onnx")
	data, err := os.ReadFile(src) // #nosec G304 -- the model path is under the module tree, not a request path
	if err != nil {
		return modelCopy{}, err
	}
	dir, err := os.MkdirTemp("", "engine-models-")
	if err != nil {
		return modelCopy{}, err
	}
	if err := os.Chmod(dir, 0o755); err != nil { // #nosec G302 -- the container user is not the host user and must traverse this directory
		_ = os.RemoveAll(dir)
		return modelCopy{}, err
	}
	add := filepath.Join(dir, "add")
	if err := os.Mkdir(add, 0o755); err != nil { // #nosec G301 -- the container user is not the host user and must traverse this directory
		_ = os.RemoveAll(dir)
		return modelCopy{}, err
	}
	file := filepath.Join(add, "model.onnx")
	if err := os.WriteFile(file, data, 0o644); err != nil { // #nosec G306 G703 -- the container user reads this bind-mounted model
		_ = os.RemoveAll(dir)
		return modelCopy{}, err
	}
	return modelCopy{root: dir, file: file, onnx: data}, nil
}

// replace writes contents over the model file so the directory watcher sees a new file.
func (m modelCopy) replace(contents []byte) error {
	before, err := fileID(m.file)
	if err != nil {
		return err
	}
	tmp := m.file + ".tmp"
	if err := os.WriteFile(tmp, contents, 0o644); err != nil { // #nosec G306 -- the container user reads this bind-mounted model
		return err
	}
	if err := os.Rename(tmp, m.file); err != nil {
		return err
	}
	after, err := fileID(m.file)
	if err != nil {
		return err
	}
	if before.same(after) {
		return fmt.Errorf("rewriting %s did not change the file identity", m.file)
	}
	return nil
}

type identity struct {
	inode   uint64
	size    int64
	modTime time.Time
}

func (a identity) same(b identity) bool {
	return a.inode == b.inode && a.size == b.size && a.modTime.Equal(b.modTime)
}

func fileID(path string) (identity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return identity{}, err
	}
	id := identity{size: info.Size(), modTime: info.ModTime()}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return identity{}, fmt.Errorf("stat %s", path)
	}
	id.inode = stat.Ino
	return id, nil
}

func checkServing(ctr *container, natsURL string) error {
	if err := ctr.waitLog("nats connected", 20*time.Second); err != nil {
		return err
	}
	base := ctr.base()
	if err := restThrees(base, "/v2/models/add/infer", "rest"); err != nil {
		return fmt.Errorf("rest: %w", err)
	}
	if err := restThrees(base, "/v2/models/add/versions/1/infer", "rest-v1"); err != nil {
		return fmt.Errorf("rest version: %w", err)
	}
	if err := restBinary(base, "rest-bin"); err != nil {
		return fmt.Errorf("binary: %w", err)
	}
	if err := grpcThrees(ctr.grpc, "", "grpc"); err != nil {
		return fmt.Errorf("grpc: %w", err)
	}
	if err := grpcThrees(ctr.grpc, "1", "grpc-v1"); err != nil {
		return fmt.Errorf("grpc version: %w", err)
	}
	if err := natsThrees(natsURL, "inference.v2.models.add.infer", "nats"); err != nil {
		return fmt.Errorf("nats: %w", err)
	}
	if err := natsThrees(natsURL, "inference.v2.models.add.versions.1.infer", "nats-v1"); err != nil {
		return fmt.Errorf("nats version: %w", err)
	}
	row, err := indexState(base, "add")
	if err != nil {
		return err
	}
	if row.State != "READY" || row.Version != "1" {
		return fmt.Errorf("index %+v", row)
	}
	status, body, err := getURL(ctr.metricsURL() + "/metrics")
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("metrics status %d", status)
	}
	text := string(body)
	for _, name := range inferenceNames {
		if !strings.Contains(text, name) {
			return fmt.Errorf("metrics missing %s", name)
		}
	}
	for _, name := range absentGPUNames {
		if strings.Contains(text, name) {
			return fmt.Errorf("metrics contains %s", name)
		}
	}
	return nil
}

func checkChain(ctr *container) error {
	lines, err := logLines(ctr.logs())
	if err != nil {
		return err
	}
	var chains int
	for _, line := range lines {
		if !strings.EqualFold(line.Level, "info") || line.Msg != "runtime" || len(line.Providers) == 0 {
			continue
		}
		chains++
		if len(line.Providers) != 1 || line.Providers[0] != "CPUExecutionProvider" {
			return fmt.Errorf("chain %v", line.Providers)
		}
	}
	if chains != 1 {
		return fmt.Errorf("chain info lines %d", chains)
	}
	return nil
}

type logLine struct {
	Level     string   `json:"level"`
	Msg       string   `json:"msg"`
	Providers []string `json:"providers"`
}

func logLines(text string) ([]logLine, error) {
	var lines []logLine
	for _, raw := range strings.Split(text, "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" || !strings.HasPrefix(raw, "{") {
			continue
		}
		var line logLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			return nil, fmt.Errorf("log line %q: %w", raw, err)
		}
		lines = append(lines, line)
	}
	return lines, nil
}

func (c *container) waitLog(msg string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		lines, err := logLines(c.logs())
		if err == nil {
			for _, line := range lines {
				if line.Msg == msg {
					return nil
				}
			}
		}
		if !c.running() {
			return fmt.Errorf("container exited before %s", msg)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("log did not contain %q", msg)
}

func checkRejections(ctr *container) error {
	base := ctr.base()
	status, body, _, err := postInfer(base+"/v2/models/add/versions/2/infer", []byte(addJSON("missing")), nil)
	if err != nil {
		return err
	}
	if status != http.StatusNotFound {
		return fmt.Errorf("version 2 status %d body %s", status, trim(body))
	}
	if _, err := errorText(body); err != nil {
		return err
	}
	status, body, _, err = postInfer(base+"/v2/models/add/infer", float32LE(1, 2, 3), map[string]string{
		"Content-Type":                    "application/octet-stream",
		"Inference-Header-Content-Length": "0",
	})
	if err != nil {
		return err
	}
	if status != http.StatusBadRequest {
		return fmt.Errorf("raw body status %d body %s", status, trim(body))
	}
	status, body, _, err = postInfer(base+"/v2/repository/models/add/load", []byte(`{}`), nil)
	if err != nil {
		return err
	}
	if status != http.StatusNotImplemented {
		return fmt.Errorf("load status %d body %s", status, trim(body))
	}
	return nil
}

// checkReload rewrites the model while infer calls are in flight.
// At least one response in the next 45 seconds is the loading sentence.
// A full queue does not count. After the model is ready again, ones plus twos is threes.
func checkReload(ctr *container, model modelCopy) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	var saw atomic.Bool
	var full atomic.Int64
	var okCount atomic.Int64
	client := &http.Client{Timeout: 5 * time.Second}
	body := []byte(addJSON("reload"))
	url := ctr.base() + "/v2/models/add/infer"
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				status, text, err := postError(client, url, body)
				if err != nil {
					continue
				}
				switch {
				case status == http.StatusOK:
					okCount.Add(1)
				case status == http.StatusServiceUnavailable && text == loadingSentence:
					saw.Store(true)
				case status == http.StatusServiceUnavailable && text == queueFullSentence:
					full.Add(1)
				}
			}
		}()
	}
	flow := time.Now().Add(10 * time.Second)
	for okCount.Load() == 0 && time.Now().Before(flow) {
		time.Sleep(20 * time.Millisecond)
	}
	if okCount.Load() == 0 {
		cancel()
		wg.Wait()
		return errors.New("infer calls were not in flight before the rewrite")
	}
	if err := model.replace(model.onnx); err != nil {
		cancel()
		wg.Wait()
		return err
	}
	deadline := time.Now().Add(loadingWait)
	for !saw.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	wg.Wait()
	if !saw.Load() {
		return fmt.Errorf("loading response was not observed within %s (%d queue-full responses do not count)", loadingWait, full.Load())
	}
	if err := waitIndex(ctr.base(), "READY", 45*time.Second); err != nil {
		return err
	}
	if err := restThrees(ctr.base(), "/v2/models/add/infer", "after-reload"); err != nil {
		return fmt.Errorf("after reload: %w", err)
	}
	return nil
}

func checkGarbage(ctr *container, model modelCopy) error {
	if err := model.replace([]byte("not-an-onnx-model")); err != nil {
		return err
	}
	if err := waitIndex(ctr.base(), "UNAVAILABLE", 45*time.Second); err != nil {
		return err
	}
	status, _, err := getURL(ctr.base() + "/v2/health/ready")
	if err != nil {
		return err
	}
	if status == http.StatusOK {
		return errors.New("server ready is true while the model is unavailable")
	}
	status, _, err = getURL(ctr.base() + "/v2/health/live")
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("live status %d", status)
	}
	if !ctr.running() {
		return errors.New("container exited after the model became unavailable")
	}
	return nil
}

func waitIndex(base, state string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		row, err := indexState(base, "add")
		if err == nil && row.State == state {
			return nil
		}
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("index state %s reason %q", row.State, row.Reason)
			if state == "READY" && row.State == "UNAVAILABLE" {
				return last
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if last == nil {
		last = errors.New("timed out")
	}
	return fmt.Errorf("model did not become %s: %w", state, last)
}

func postError(client *http.Client, url string, body []byte) (int, string, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", err
	}
	if resp.StatusCode == http.StatusOK {
		return resp.StatusCode, "", nil
	}
	text, err := errorText(payload)
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, text, nil
}

func checkRefusedNATS(image, models, creds string) error {
	port, err := closedPort()
	if err != nil {
		return err
	}
	name := "engine-image-test-nats"
	ctr, err := startEngine(name, image, engineSpec{
		models:  models,
		creds:   creds,
		natsURL: "nats://host.docker.internal:" + strconv.Itoa(port),
		publish: true,
	})
	if err != nil {
		return err
	}
	defer ctr.remove()
	if err := ctr.waitLive(30 * time.Second); err != nil {
		return ctr.fail(err)
	}
	if err := ctr.waitReady(30 * time.Second); err != nil {
		return ctr.fail(err)
	}
	if err := ctr.waitLog("nats connection attempt failed, retrying", 20*time.Second); err != nil {
		return ctr.fail(err)
	}
	lines, err := logLines(ctr.logs())
	if err != nil {
		return ctr.fail(err)
	}
	for _, line := range lines {
		if line.Msg == "nats connected" {
			return ctr.fail(errors.New("connected to a closed port"))
		}
	}
	if !ctr.running() {
		return ctr.fail(errors.New("container exited"))
	}
	return nil
}

func checkMissingLibrary(image string) error {
	name := "engine-image-test-library"
	ctr, err := startEngine(name, image, engineSpec{
		publish: true,
		libPath: "/opt/onnxruntime/lib/missing.so",
	})
	if err != nil {
		return err
	}
	defer ctr.remove()
	if err := ctr.waitLive(30 * time.Second); err != nil {
		return ctr.fail(err)
	}
	status, _, err := getURL(ctr.base() + "/v2/health/ready")
	if err != nil {
		return ctr.fail(err)
	}
	if status == http.StatusOK {
		return ctr.fail(errors.New("ready is 200 with a missing library"))
	}
	if !ctr.running() {
		return ctr.fail(errors.New("container exited"))
	}
	return nil
}
