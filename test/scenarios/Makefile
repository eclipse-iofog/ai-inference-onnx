IMAGE := emirhandurmus/ai-inference-onnx-scenarios
VERSION ?= 0.1.0
PROTO := ../../api/openinference/v2/open_inference_grpc.proto

.PHONY: docker docker-multi

# Build the scenario client for this machine.
# Exec into the container and run:
#   python3 /opt/scenarios/rest_metadata.py
#   python3 /opt/scenarios/rest_infer.py [image|--all]
#   python3 /opt/scenarios/grpc_metadata.py
#   python3 /opt/scenarios/grpc_infer.py [image|--all]
#   python3 /opt/scenarios/nats_metadata.py
#   python3 /opt/scenarios/nats_infer_rendezvous.py [image|--all]
# The default image is /opt/scenarios/assets/wafers/wafer-scratch-001.jpg.
docker:
	mkdir -p .build/proto
	cp $(PROTO) .build/proto/open_inference_grpc.proto
	docker build -f Dockerfile -t $(IMAGE):$(VERSION) .

# Build linux/amd64 and linux/arm64. This does not push.
docker-multi-push:
	mkdir -p .build/proto
	cp $(PROTO) .build/proto/open_inference_grpc.proto
	docker buildx build --push --platform linux/amd64,linux/arm64 \
		-f Dockerfile -t $(IMAGE):$(VERSION) .
