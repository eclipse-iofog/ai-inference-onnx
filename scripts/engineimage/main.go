// Command engineimage calls a CPU engine image over REST, gRPC, and NATS.
// It starts an embedded NATS server, mints a credentials file, and does not load ONNX Runtime.
package main

import (
	"fmt"
	"os"
	"time"
)

const (
	loadingSentence   = "model is loading"
	queueFullSentence = "request queue for this model is full"
	libPath           = "/opt/onnxruntime/lib/libonnxruntime.so.1.31.0"
	loadingWait       = 45 * time.Second
)

func main() {
	if err := run(); err != nil {
		note("engine image test: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if err := os.Unsetenv("ORT_LIB_PATH"); err != nil {
		return err
	}
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	image, err := resolveImage(root)
	if err != nil {
		return err
	}
	note("image %s\n", image)
	if err := inspectImage(image); err != nil {
		return err
	}
	if err := shellEntrypointRuns(image); err != nil {
		return err
	}

	model, err := copyAddModel(root)
	if err != nil {
		return err
	}
	defer os.RemoveAll(model.root)

	nats, err := startNATS()
	if err != nil {
		return err
	}
	defer nats.close()

	mainName := "engine-image-test"
	removeContainer(mainName)
	ctr, err := startEngine(mainName, image, engineSpec{
		models:  model.root,
		creds:   nats.credsPath,
		natsURL: nats.containerURL,
		publish: true,
	})
	if err != nil {
		return err
	}
	defer ctr.remove()

	if err := ctr.waitReady(60 * time.Second); err != nil {
		return ctr.fail(err)
	}
	if err := checkServing(ctr, nats.clientURL); err != nil {
		return ctr.fail(err)
	}
	if err := checkChain(ctr); err != nil {
		return ctr.fail(err)
	}
	if err := checkRejections(ctr); err != nil {
		return ctr.fail(err)
	}
	if err := checkReload(ctr, model); err != nil {
		return ctr.fail(err)
	}
	if err := checkGarbage(ctr, model); err != nil {
		return ctr.fail(err)
	}
	ctr.remove()

	// The refused dial still has a model that can serve.
	if err := model.replace(model.onnx); err != nil {
		return err
	}
	if err := checkRefusedNATS(image, model.root, nats.credsPath); err != nil {
		return err
	}
	return checkMissingLibrary(image)
}

func note(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format, args...)
}
