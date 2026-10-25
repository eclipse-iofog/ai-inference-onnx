IMAGE := emirhandurmus/ai-inference-onnx-turbofan
MODEL := docker.io/emirhandurmus/turbofan-rul
VERSION ?= 0.1.0
PROTO := ../../../api/openinference/v2/open_inference_grpc.proto
# PyTorch publishes wheels through Python 3.13. python3 on this machine may be newer.
PYTHON ?= python3.12
VENV := .venv

.PHONY: train model-push docker

# Fit on data/train_FD001.txt through train_FD004.txt.
# Writes model/turbofan-rul.onnx, model/config.pbtxt, and assets/.
train:
	test -x $(VENV)/bin/python || $(PYTHON) -m venv $(VENV)
	$(VENV)/bin/pip install -r requirements-train.txt
	$(VENV)/bin/python train.py

# Maintainer-only. Publishes the ONNX file and config.pbtxt. Does not run from a local test build.
model-push:
	test -f model/turbofan-rul.onnx
	test -f model/config.pbtxt
	cd model && oras push "$(MODEL):$(VERSION)" \
		--artifact-type application/vnd.cncf.model.manifest.v1+json \
		"./turbofan-rul.onnx:application/vnd.cncf.model.weight.v1.raw" \
		"./config.pbtxt:application/octet-stream"
	oras tag "$(MODEL):$(VERSION)" "$(MODEL):latest"

# Build the scenario client for this machine. This does not push.
# Exec into the container and run:
#   python3 /opt/scenarios/rest_metadata.py
#   python3 /opt/scenarios/rest_infer.py [engine|--all]
#   python3 /opt/scenarios/grpc_metadata.py
#   python3 /opt/scenarios/grpc_infer.py [engine|--all]
#   python3 /opt/scenarios/nats_metadata.py
#   python3 /opt/scenarios/nats_infer_rendezvous.py [engine|--all]
# The default engine is fd004-031.
docker:
	test -f assets/windows.json
	test -f assets/scaler.json
	mkdir -p .build/proto
	cp $(PROTO) .build/proto/open_inference_grpc.proto
	docker build -f Dockerfile -t $(IMAGE):$(VERSION) .
