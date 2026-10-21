//go:build cgo

package natsapi

import (
	"os"
	"strconv"
	"testing"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/ort"
)

func TestRendezvousAddModel(t *testing.T) {
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
	srv := startEmbedded(t)
	url := srv.ClientURL()
	pool := &countingPool{inner: poolAdapter{models.pool}}
	serveUntilCleanup(t, url, Config{
		Catalog:         models,
		Runner:          models,
		Ready:           models.ready,
		MaxPayloadBytes: 32 << 20,
		Buffers:         pool,
		After:           newManualClock().After,
	})
	client := connect(t, url)
	seen := watchModels(t, connect(t, url))

	doc, suffix := splitBinary(t, "add-rendezvous")
	subject, id := accepted(t, request(t, client, "inference.v2.models.add.infer", rendezvousHeader(strconv.Itoa(len(suffix))), doc))
	subs := uploadSubscriptions(t, srv)
	if len(subs) != 1 || subs[0].Subject != subject || subs[0].Queue != "" {
		t.Fatalf("upload subscriptions %+v, want %s with no queue group", subs, subject)
	}
	wantAcked(t, sendSlice(t, client, subject, id, suffix[:16]))
	wantAcked(t, sendSlice(t, client, subject, id, suffix[16:32]))
	assertThrees(t, sendSlice(t, client, subject, id, suffix[32:]), "add-rendezvous", true)

	pool.waitReturned(t, 1)
	pool.assertSuffixInPoolBuffer(t, doc, suffix)
	noResponders(t, client, subject)
	if got := seen(); len(got) != 1 || got[0] != "inference.v2.models.add.infer" {
		t.Fatalf("the model wildcard saw %v, want only the header", got)
	}
}
