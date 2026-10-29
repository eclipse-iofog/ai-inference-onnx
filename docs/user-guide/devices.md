# Choose an image and pass a device

The image tag selects the execution provider. There is no configuration key for it. ONNX Runtime is 1.31.0 in every engine image. The variant table, the architectures, and the SDK versions are in [Images](../reference/images.md).

After the library loads, the engine uses the first provider that library reports, in this order: TensorRT, then CUDA, then MIGraphX, OpenVINO, QNN, and otherwise CPU. It logs the chain once at start, on the `runtime` line. If that provider cannot start, the model is `UNAVAILABLE` with the reason, and the engine does not retry on CPU. After the provider has started, an unsupported node may still run on CPU. That is graph partitioning.

`cpu` is the image in [Run the container](docker-run.md). Use it until the model needs an accelerator. `openvino` also runs on the CPU device and needs no device node.

## Keep the image user

The process runs as `10001:10001`. `/var/cache/inference` and `/home/inference` are created for that user, mode `0750`. `runAsUser` and `runAsGroup` stay `"10001"`. A device group is an extra group, not a replacement for `10001`.

The device node on the host is group-owned. A non-root process opens it when that host group id is on the container. Group ids differ between hosts. Read them on the node that will run the container:

```bash
getent group render
getent group video
```

The number in the third field is the id you pass. Docker and Podman take `--group-add <gid>` once per id. The catalog field is `supplementalGroups`, an array of those integers.

| Image | What you pass | `supplementalGroups` |
|---|---|---|
| `cpu`, `openvino` | Nothing | `[]` |
| `cuda12`, `cuda13`, `tensorrt12`, `tensorrt13` | The NVIDIA container toolkit (`--gpus` or CDI). A Jetson board may need the `video` group | The host ids that own the device nodes. Often `render`, `video`, or both |
| `migraphx` | `/dev/kfd` and `/dev/dri` | The numeric ids of `video` and `render` on that host |
| `qnn` | The device nodes the board vendor assigns | The group id the board vendor assigns |

`cuda12` on arm64 is NVIDIA server aarch64 (SBSA). It does not run on Jetson Orin with JetPack 6. `cuda13` and `tensorrt13` on arm64 cover SBSA and JetPack 7. There is no arm64 `tensorrt12`. A JetPack 6 image is not published.

```bash
docker run --rm --read-only --tmpfs /tmp \
  --group-add 44 \
  --gpus all \
  -e AUTH_MODE=disabled \
  -p 8000:8000 -p 8001:8001 -p 8002:8002 \
  -v "$PWD/models:/models:ro" \
  -v engine-cache:/var/cache/inference \
  ghcr.io/eclipse-iofog/ai-inference-onnx:0.1.0-ort1.31.0-cuda12
```

`--group-add` keeps the primary group at `10001`. Do not pass `--user 10001:44`. That replaces the primary group.

On a catalog the same ids sit beside the image user. `cdiDevices` names the device. `supplementalGroups` carries the host group ids. A CDI spec that lists `additionalGids` does not replace `supplementalGroups`.

```yaml
container:
  hostNetworkMode: false
  isPrivileged: false
  runAsUser: "10001"
  runAsGroup: "10001"
  supplementalGroups: [44]
  readOnlyRootFilesystem: true
  cdiDevices:
    - nvidia.com/gpu=all
```

`migraphx` lists both ids, for example `[44, 993]`, using the numbers from that host. One CDI device that must add more than one group still lists every id in `supplementalGroups`. `runAsGroup` holds a single group, and that group stays `10001`.

## Mount a writable cache

`readOnlyRootFilesystem: true` makes the copy of `/var/cache/inference` inside the image unwritable. The engine still writes there.

- `XDG_CACHE_HOME` is `/var/cache`. TensorRT's engine cache and timing cache are `/var/cache/inference/tensorrt`. The cache survives a restart only when this path is a volume.
- `CUDA_CACHE_PATH` is `/var/cache/inference/cuda`.

The mount is read-write. A read-only mount leaves TensorRT and CUDA unable to create their directories. Models and knowledge stay read-only.

```yaml
volumes:
  - hostDestination: engine-cache
    containerDestination: /var/cache/inference
    accessMode: rw
    type: volume
    scope: private
```

`scope: private` keeps one engine's cache. The volume must be writable by uid `10001`. A fresh volume owned by root, with no write bit for that uid, fails the same way a missing mount fails: the provider or the session cannot create `cuda` or `tensorrt`, and the model stays `UNAVAILABLE` with the reason.

`/tmp` is a tmpfs. The image's `/tmp` is on the root filesystem, so a read-only root leaves it unwritable.

```yaml
tmpfs:
  - containerPath: /tmp
    size: 64
    mode: "1777"
```

Docker is `--tmpfs /tmp`. The named volume has no `:ro`.

`/home/inference` is the home directory. The platform mounts the NATS credentials file there read-only when the catalog sets `natsAccess: true`. Add another volume only when a path under the read-only root must accept writes. The cache path above is the one the engine writes.

The next page puts these fields into an application: [Bind a model from Edgelet](edgelet-bind.md).
