# Images

Each GitHub remote publishes its own names on GHCR. A tag on one GitHub remote publishes only that remote's names.

| Image | `eclipse-iofog/ai-inference-onnx` | `datasance/ai-inference-onnx` |
|---|---|---|
| Engine | `ghcr.io/eclipse-iofog/ai-inference-onnx` | `ghcr.io/datasance/ai-inference-onnx` |
| Dashboard | `ghcr.io/eclipse-iofog/ai-inference-dashboard` | `ghcr.io/datasance/ai-inference-dashboard` |
| Runtime | `ghcr.io/eclipse-iofog/ai-inference-onnx-runtime` | `ghcr.io/datasance/ai-inference-onnx-runtime` |

`datasance/ai-inference-onnx` is a fork. The tag shape is the same on each remote.

## Tags

A git tag `v0.1.0` publishes `0.1.0-ort1.31.0-<variant>` on that remote's engine name. The `v` is stripped. The architecture is not in the tag. There is no `latest` tag. `0.1.0-ort1.31.0-cpu` is the manifest for amd64, arm64, and riscv64. The catalog default is `cpu`.

A git tag `dashboard/v0.1.0` publishes `0.1.0` on that remote's dashboard name. The dashboard tag has no ONNX Runtime token. It does not contain `ort1.31.0`.

The runtime image `ai-inference-onnx-runtime` is tagged `1.31.0-<variant>`. It is a build input. Nodes do not pull it. A maintainer publishes it with `make runtime-arch` and `make runtime-index`. Both registries serve that digest. GitHub signs the recorded digest and does not build the image.

## Variants

ONNX Runtime is 1.31.0 in every engine image. The image tag selects the provider. There is no configuration key for it.

| Variant | Platforms | SDK |
|---|---|---|
| `cpu` | amd64, arm64, and riscv64 | No accelerator SDK |
| `cuda12` | amd64, and arm64 on SBSA | CUDA 12.8.1, cuDNN 9.8.0.87 |
| `cuda13` | amd64, and arm64 on SBSA and JetPack 7 | CUDA 13.0.2, cuDNN 9.14.0.64 |
| `tensorrt12` | amd64 | TensorRT 10.14.1.48, then CUDA. CUDA 12.9.1, cuDNN 9.10.2.21 |
| `tensorrt13` | amd64, and arm64 on SBSA and JetPack 7 | amd64 is TensorRT 10.14.1.48, CUDA 13.0.2, cuDNN 9.14.0.64. arm64 is TensorRT 10.15.1.29, CUDA 13.1.2, cuDNN 9.17.1.4 |
| `qnn` | arm64 | QNN 2.51.0.260929, HTP backend |
| `openvino` | amd64 | OpenVINO 2026.2.1 |
| `migraphx` | amd64 | MIGraphX on ROCm 7.1 |

`cuda12` on arm64 is for NVIDIA's server aarch64 platform (SBSA: Grace, GH200, or an Arm server with a discrete GPU). It does not run on Jetson Orin with JetPack 6. `cuda13` and `tensorrt13` on arm64 cover SBSA and JetPack 7. There is no arm64 `tensorrt12`. A JetPack 6 image is not published.

## Provider selection

After the library loads, the engine reads the providers that library reports and uses the first match, in this order:

1. TensorRT, then CUDA.
2. CUDA.
3. MIGraphX.
4. OpenVINO.
5. QNN.
6. Otherwise CPU.

It logs the chain once at start, on the `runtime` line in `providers`, for example `CPUExecutionProvider`. If that provider cannot start, the model is `UNAVAILABLE` with the reason, and the engine does not retry on CPU. After the provider has started, an unsupported node may still run on CPU. That is graph partitioning. It is not a fallback when the device library failed to load.

OpenVINO runs on the CPU device. TensorRT keeps its engine cache and its timing cache under `/var/cache/inference/tensorrt`. CUDA writes `/var/cache/inference/cuda`. Both paths are writable only when `/var/cache/inference` is a read-write volume. The walkthrough is in [Choose an image and pass a device](../user-guide/devices.md).

## Non-root

The process runs as `10001:10001`. It needs no capability, and it listens above 1024. `runAsUser` and `runAsGroup` stay `"10001"`. Run it with `--read-only --tmpfs /tmp`. The model bind and the knowledge bind are read-only. A UBI image includes a shell and `curl`, so an exec session and a command probe can start. The riscv64 image does not.

`readOnlyRootFilesystem` makes the image copy of `/var/cache/inference` unwritable. Mount a private named volume there, `accessMode: rw`, writable by uid `10001`. A root-owned empty volume cannot create `cuda` or `tensorrt`.

The volume sits on the microservice. The tmpfs sits on `container`.

```yaml
volumes:
  - hostDestination: engine-cache
    containerDestination: /var/cache/inference
    accessMode: rw
    type: volume
    scope: private
```

```yaml
tmpfs:
  - containerPath: /tmp
    size: 64
    mode: "1777"
```

`/home/inference` is the home directory. The platform mounts the NATS credentials file there read-only. Add another volume only when a path under the read-only root must accept writes.

## Devices and groups

A device node on the host is group-owned. The process opens it when that host group id is in `supplementalGroups`. The primary group stays `10001`. Group ids differ between hosts. Read them on the node with `getent group render` and `getent group video`.

| Image | What the operator passes | `supplementalGroups` |
|---|---|---|
| `cpu`, `openvino` | Nothing | `[]` |
| `cuda12`, `cuda13`, `tensorrt12`, `tensorrt13` | The NVIDIA container toolkit (`--gpus` or CDI). A Jetson board may need the `video` group | The host ids that own the device nodes |
| `migraphx` | `/dev/kfd` and `/dev/dri` | The numeric ids of `video` and `render` on that host |
| `qnn` | The device nodes the board vendor assigns | The group id the board vendor assigns |

Docker and Podman take `--group-add <gid>` once per id. That flag adds a supplemental group. `--user 10001:44` would replace the primary group. Leave the image user in place.

```bash
docker run --read-only --tmpfs /tmp \
  --group-add 44 --gpus all \
  -v engine-cache:/var/cache/inference \
  ghcr.io/eclipse-iofog/ai-inference-onnx:0.1.0-ort1.31.0-cuda12
```

```yaml
runAsUser: "10001"
runAsGroup: "10001"
supplementalGroups: [44]
cdiDevices:
  - nvidia.com/gpu=all
```

`supplementalGroups` is the catalog field for those host ids. A CDI spec that lists `additionalGids` does not replace it. More than one group is more than one integer in the array. `runAsGroup` holds a single group, and that group stays `10001`. The catalog block is in [Catalog](catalog.md).

## Licenses and archives

`/licenses` in every image holds the license and the third-party notices.

The repository root carries `SBOM.md`, `LICENSE-3RD-PARTY.md`, `sbom.cdx.json`, and `sbom.spdx.json` for the source tree. The JSON files list required and optional components that have a version. Excluded modules stay in `SBOM.md` and `LICENSE-3RD-PARTY.md`. Regenerate them with `make sbom`. The engine image SBOM is the registry attestation on the pushed image. A release adds one CycloneDX file and one SPDX file for each CPU runtime archive, named `onnxruntime-1.31.0-cpu-linux-<arch>`. Those files add the file hashes. Publishing a runtime image writes that pair next to the architecture digest.

Accelerator runtimes are not bare archives. They ship inside the image. The CPU runtime is also published as three archives, one per architecture:

- `onnxruntime-1.31.0-linux-amd64.tar.gz`
- `onnxruntime-1.31.0-linux-arm64.tar.gz`
- `onnxruntime-1.31.0-linux-riscv64.tar.gz`

Each archive is `lib/`, `include/`, `licenses/`, `VERSION`, and `COMMIT` from the CPU runtime image. A release verifies that image before it copies the files out. It does not compile them again.
