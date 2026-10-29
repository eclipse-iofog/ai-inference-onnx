# Catalog

One application places the engine and the dashboard. The engine does not register itself with the Controller. Replace `agent.name` and `namespace` with the agent and the namespace you deploy to. The model `wafer-defect` and the knowledge item `wafer-labels` are already on that agent. This file binds them. It does not contain a secret. The platform injects the NATS URL and the credentials file when `natsAccess` is true.

The walkthrough of each block is [Bind a model from Edgelet](../user-guide/edgelet-bind.md). Image tags are in [Images](images.md). `AUTH_MODE` is in [Configuration](configuration.md). Device group ids are in [Choose an image and pass a device](../user-guide/devices.md).

This example is the CPU image. `supplementalGroups` is empty. A CUDA, TensorRT, MIGraphX, or QNN image replaces `[]` with the host group ids from that agent and keeps `runAsUser` and `runAsGroup` at `"10001"`. The cache volume is read-write. Models and knowledge stay read-only. `/tmp` is a tmpfs because `readOnlyRootFilesystem` is true.

The engine user publishes `inference.metrics.engines.>` and subscribes to `inference.v2.>` and `inference.xfer.>`. The dashboard user only subscribes to `inference.metrics.engines.>`. The dashboard microservice has no model bind and no cache volume. The engine listens on 8000, 8001, and 8002. The dashboard listens on 8080.

A datasance clone uses `ghcr.io/datasance/ai-inference-onnx` and `ghcr.io/datasance/ai-inference-dashboard` with the same tags.

```yaml
apiVersion: datasance.com/v3
kind: NatsUserRule
metadata:
  name: engine-user
  namespace: my-ecn
spec:
  pubAllow:
    - "inference.metrics.engines.>"
  subAllow:
    - "inference.v2.>"
    - "inference.xfer.>"
---
apiVersion: datasance.com/v3
kind: NatsUserRule
metadata:
  name: dashboard-user
  namespace: my-ecn
spec:
  subAllow:
    - "inference.metrics.engines.>"
---
apiVersion: datasance.com/v3
kind: Application
metadata:
  name: inference
  namespace: my-ecn
spec:
  natsConfig:
    natsAccess: true
  microservices:
    - name: engine
      agent:
        name: edge-01
      images:
        registry: remote
        amd64: ghcr.io/eclipse-iofog/ai-inference-onnx:0.1.0-ort1.31.0-cpu
        arm64: ghcr.io/eclipse-iofog/ai-inference-onnx:0.1.0-ort1.31.0-cpu
        riscv64: ghcr.io/eclipse-iofog/ai-inference-onnx:0.1.0-ort1.31.0-cpu
      models:
        bindPath: /models
        permissions: ro
        items:
          - name: wafer-defect
      knowledge:
        bindPath: /knowledge
        permissions: ro
        items:
          - name: wafer-labels
      volumes:
        - hostDestination: engine-cache
          containerDestination: /var/cache/inference
          accessMode: rw
          type: volume
          scope: private
      container:
        hostNetworkMode: false
        isPrivileged: false
        runAsUser: "10001"
        runAsGroup: "10001"
        supplementalGroups: []
        readOnlyRootFilesystem: true
        tmpfs:
          - containerPath: /tmp
            size: 64
            mode: "1777"
        env:
          - key: AUTH_MODE
            value: edgelet
        ports:
          - internal: 8000
            external: 8000
            protocol: tcp
          - internal: 8001
            external: 8001
            protocol: tcp
          - internal: 8002
            external: 8002
            protocol: tcp
      natsConfig:
        natsAccess: true
        natsRule: engine-user
    - name: dashboard
      agent:
        name: edge-01
      images:
        registry: remote
        amd64: ghcr.io/eclipse-iofog/ai-inference-dashboard:0.1.0
        arm64: ghcr.io/eclipse-iofog/ai-inference-dashboard:0.1.0
      container:
        runAsUser: "10001"
        runAsGroup: "10001"
        ports:
          - internal: 8080
            external: 8080
            protocol: tcp
      natsConfig:
        natsAccess: true
        natsRule: dashboard-user
```
