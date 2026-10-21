# Run the container

Publish REST on 8000, gRPC on 8001, and metrics on 8002. Set `AUTH_MODE=disabled`. Mount the model directory read-only at `/models`. The image already runs as `10001:10001` and needs no capability.

```bash
docker run --read-only --tmpfs /tmp \
  -e AUTH_MODE=disabled \
  -p 8000:8000 \
  -p 8001:8001 \
  -p 8002:8002 \
  -v "$PWD/models:/models:ro" \
  ghcr.io/eclipse-iofog/ai-inference-onnx:0.1.0-ort1.31.0-cpu
```

`podman run` is the same image and the same variables. A missing `AUTH_MODE` exits the process.

Device groups, the other image names, and the writable paths `/var/cache/inference` and `/home/inference` are on the [images](../reference/images.md) page. Server settings are in [configuration](../reference/configuration.md).
