//go:build cgo

package natsapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/ort"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/session"
)

func TestInferAddModel(t *testing.T) {
	if os.Getenv("ORT_LIB_PATH") == "" {
		t.Fatal("ORT_LIB_PATH is unset")
	}
	if err := ort.Init(); err != nil {
		t.Fatal(err)
	}
	version, err := ort.GetVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.31.0" {
		t.Fatalf("onnx runtime version %q", version)
	}

	models := loadAddModel(t)
	if len(models.Models()) != 1 || models.Models()[0].State != batch.StateReady {
		t.Fatalf("models %+v", models.Models())
	}
	url := startServer(t, 0)
	serve(t, url, Config{Catalog: models, Runner: models, Ready: models.ready, MaxPayloadBytes: 32 << 20})
	client := connect(t, url)

	wantStatus(t, request(t, client, "inference.v2.health.ready", nil, nil), 200)
	for _, subject := range []string{"inference.v2.models.add.infer", "inference.v2.models.add.versions.1.infer"} {
		assertThrees(t, request(t, client, subject, nil, addJSONBody("add-json")), "add-json", false)
		body, header := addBinaryBody("add-binary")
		assertThrees(t, request(t, client, subject, header, body), "add-binary", true)
	}
	for _, subject := range []string{"inference.v2.models.add.versions.2.infer", "inference.v2.models.add.versions.latest.infer"} {
		got := request(t, client, subject, nil, addJSONBody("x"))
		wantStatus(t, got, 404)
		got.errorText(t)
	}
	got := request(t, client, "inference.v2.models.add.infer", nil, []byte(`{"inputs":[{"name":"A","shape":[2,3],"datatype":"FP32","data":[1,2]}]}`))
	wantStatus(t, got, 400)
	got.errorText(t)
}

// addModels serves a temp copy of the committed add model through one queue per version.
type addModels struct {
	repo *session.Repository
	pool *session.Pool

	mu     sync.Mutex
	queues map[batch.Key]*batch.Batcher
}

func loadAddModel(t *testing.T) *addModels {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("..", "..", "testdata", "models", "add", "model.onnx")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(root, "add"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "add", "model.onnx"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	pool, err := session.NewPool(32 << 20)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := session.Load(session.Config{
		ModelsRoot:    root,
		KnowledgeRoot: t.TempDir(),
		IntraOp:       1,
		InterOp:       1,
		Providers:     []ort.Provider{{Name: ort.CPUExecutionProvider}},
	}, pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := repo.Close(); err != nil {
			t.Error(err)
		}
	})
	return &addModels{repo: repo, pool: pool, queues: make(map[batch.Key]*batch.Batcher)}
}

func (m *addModels) ready() bool {
	for _, model := range m.repo.Models() {
		if model.State == session.Ready {
			return true
		}
	}
	return false
}

func (m *addModels) Models() []batch.Directory {
	var out []batch.Directory
	for _, model := range m.repo.Catalog() {
		dir := batch.Directory{Name: model.Name, State: string(model.State), Reason: model.Reason}
		for _, v := range model.Versions {
			dir.Versions = append(dir.Versions, batch.ModelVersion{
				Version: v.Version,
				Signature: batch.Signature{
					Inputs:       specs(v.Inputs),
					Outputs:      specs(v.Outputs),
					MaxBatchSize: v.MaxBatchSize,
				},
			})
		}
		out = append(out, dir)
	}
	return out
}

func specs(tensors []session.CatalogTensor) []batch.TensorSpec {
	out := make([]batch.TensorSpec, len(tensors))
	for i, tensor := range tensors {
		out[i] = batch.TensorSpec{Name: tensor.Name, Datatype: batch.Datatype(tensor.Datatype), Shape: tensor.Shape}
	}
	return out
}

func (m *addModels) Run(ctx context.Context, name, version string, req *batch.Request) (*batch.Reply, error) {
	b, err := m.queue(name, version)
	if err != nil {
		return nil, err
	}
	return b.Infer(ctx, req)
}

func (m *addModels) queue(name, version string) (*batch.Batcher, error) {
	key := batch.Key{Model: name, Version: version}
	m.mu.Lock()
	defer m.mu.Unlock()
	if b := m.queues[key]; b != nil {
		return b, nil
	}
	b, err := batch.New(batch.Config{
		Key:      key,
		Capacity: 8,
		Admit: func() error {
			admission := m.repo.Admit(name, version)
			if admission.Proceed() {
				return nil
			}
			if err := batch.GateError(admission.Status, admission.Reason); err != nil {
				return err
			}
			return batch.NotFound(fmt.Sprintf("model %q was not found", name))
		},
		Pool: poolAdapter{m.pool},
		Slot: m.slot(name, version),
	})
	if err != nil {
		return nil, err
	}
	m.queues[key] = b
	return b, nil
}

func (m *addModels) slot(name, version string) batch.Slot {
	return func(ctx context.Context, inputs []batch.Tensor) ([]batch.Tensor, func(), error) {
		converted := make([]session.Input, len(inputs))
		for i, in := range inputs {
			converted[i] = session.Input{Name: in.Name, Shape: in.Shape, Data: in.Data}
		}
		outputs, err := m.repo.Run(ctx, name, version, converted)
		if err != nil {
			var refused *session.StatusError
			if errors.As(err, &refused) {
				if mapped := batch.GateError(refused.Status, refused.Reason); mapped != nil {
					return nil, nil, mapped
				}
			}
			return nil, nil, err
		}
		tensors := make([]batch.Tensor, len(outputs))
		for i, out := range outputs {
			tensors[i] = batch.Tensor{Name: out.Name, Shape: out.Shape, Data: out.Buffer.Data}
		}
		return tensors, func() {
			for _, out := range outputs {
				m.repo.Release(out)
			}
		}, nil
	}
}

type poolAdapter struct {
	pool *session.Pool
}

func (p poolAdapter) Get(n int) ([]byte, error) {
	buf, err := p.pool.Get(n, nil)
	if err != nil {
		return nil, err
	}
	return buf.Data, nil
}

func (p poolAdapter) Put(b []byte) {
	p.pool.Put(session.Buffer{Data: b, Pooled: true})
}
