# Bind a model from Edgelet

This page is the application you deploy after the local path works. The engine does not register itself with the Controller. Replace `agent.name` and `namespace` with the agent and the namespace you deploy to. The model `wafer-defect` and the knowledge item `wafer-labels` are already on that agent. This file binds them. It does not contain a secret. The platform injects the NATS URL and the credentials file when `natsAccess` is true.

The image on this page is the CPU tag. A device image uses the same container fields, with `supplementalGroups` filled from [Choose an image and pass a device](devices.md). The copy-paste file, without this commentary, is [Catalog](../reference/catalog.md).

## NATS rules

The engine user publishes metrics and subscribes to requests and rendezvous uploads. The dashboard user only subscribes to metrics. A caller that publishes infer needs its own user, with publish on `inference.v2.>` and on `inference.xfer.>`. That caller is not this application.

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
```

## The application

`AUTH_MODE` is `edgelet`. The platform mounts the public key. The container stays unprivileged, on uid and gid `10001`, with a read-only root, a tmpfs on `/tmp`, and a private read-write volume at `/var/cache/inference`. `supplementalGroups` is empty because this image is `cpu`. A CUDA, TensorRT, MIGraphX, or QNN image replaces `[]` with the host group ids from that agent.

Models and knowledge are read-only binds. The engine listens on 8000, 8001, and 8002. The dashboard listens on 8080, runs as `10001:10001`, and has no model bind and no cache volume.

```yaml
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

A datasance image uses `ghcr.io/datasance/ai-inference-onnx` and `ghcr.io/datasance/ai-inference-dashboard` with the same tags. Image tags are in [Images](../reference/images.md).

After the application is running, `GET /v2/health/live` is 200 from the node. `GET /v2/health/ready` is 200 when `wafer-defect` is `READY`. Protected calls need the Edgelet token from [Choose authentication and TLS](auth-tls.md). Callers on another node do not use that token. They publish on NATS.

The riscv64 engine image has no shell and no `curl`. A command probe that runs `curl` inside the container works on the UBI image and fails on riscv64. Probe `GET /v2/health/live` over HTTP from outside the container when the node is riscv64.

The next page is [Call infer on NATS](infer-nats.md).
