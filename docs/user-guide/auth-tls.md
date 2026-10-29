# Choose authentication and TLS

`AUTH_MODE` is required. If neither the environment nor `/etc/inference/config.yaml` sets it, the process exits. The mode applies to REST and gRPC. It does not apply to NATS. NATS account and user rules authenticate that connection. The permission table and the token claims are in [Authentication](../reference/auth.md). The certificate keys are in [Configuration](../reference/configuration.md).

These stay open in every mode:

- `GET /v2/health/live` and `GET /v2/health/ready`
- gRPC `ServerLive` and `ServerReady`
- `GET /metrics` on port 8002

A scraper has no inference credential. The model names on the metrics port are operational metadata. Infer, model metadata, model ready, the model list, server metadata, and the repository index are protected.

## Pick a mode

| Mode | Use it when | Credential |
|---|---|---|
| `disabled` | `docker run`, `podman run`, and CI | None |
| `static` | One shared bearer for every protected call | `Authorization: Bearer` equal to `AUTH_STATIC_TOKEN` |
| `edgelet` | A catalog on a node | An Edgelet service-account JWT |

`disabled` performs no check. The container on [Run the container](docker-run.md) uses it. Do not publish that port beyond the machine.

## A shared bearer

`static` compares the bearer in constant time. The token is at least 32 characters, is not a JWT, has no per-model rules, and is never logged. A missing or short token exits the process. Changing it requires a restart. Presenting it allows every protected call.

```bash
docker run --rm --read-only --tmpfs /tmp \
  -e AUTH_MODE=static \
  -e AUTH_STATIC_TOKEN="0123456789abcdef0123456789abcdef" \
  -p 8000:8000 -p 8001:8001 -p 8002:8002 \
  -v "$PWD/models:/models:ro" \
  -v engine-cache:/var/cache/inference \
  ghcr.io/eclipse-iofog/ai-inference-onnx:0.1.0-ort1.31.0-cpu
```

```bash
curl -sS -X POST "http://127.0.0.1:8000/v2/models/add/infer" \
  -H "Authorization: Bearer 0123456789abcdef0123456789abcdef" \
  -H "Content-Type: application/json" \
  -d '{"inputs":[{"name":"A","datatype":"FP32","shape":[2,3],"data":[1,1,1,1,1,1]},{"name":"B","datatype":"FP32","shape":[2,3],"data":[2,2,2,2,2,2]}]}'
```

gRPC sends the same value in metadata: `-H 'Authorization: Bearer 0123456789abcdef0123456789abcdef'` on the `grpcurl` command from [Call infer on gRPC](infer-grpc.md).

A missing or invalid token is 401. The body is `{"error":"..."}` and does not echo the token. Health still answers.

## A token minted on the node

`edgelet` verifies an Edgelet service-account JWT. The engine watches `/var/run/secrets/edgelet.iofog.org/serviceaccount/edgelet.jwk` and swaps the public key in memory when the file changes. A restart is not required. The key is a public Ed25519 JWK (`kty` `OKP`, `crv` `Ed25519`, `alg` `EdDSA`). A missing or unreadable key at startup exits the process. After startup, a missing key fails new REST and gRPC calls until a valid key is loaded again.

A token passes when all of the following hold:

1. The signature matches the current `edgelet.jwk`, algorithm EdDSA.
2. `tokenUse` is `serviceaccount`, and `iat`, `nbf`, `exp`, and `iss` are valid. The issuer is `https://edgelet.default.svc.bridge.local`.
3. `rulesByGroup` allows the operation under the group `inference.iofog.org/v2`.

The `aud` claim is the Edgelet bridge, `https://edgelet.default.svc.bridge.local`. The engine accepts that audience. Each node has its own signing key, so a token from another node fails verification. Revocation is the token `exp` window. The engine does not call Edgelet on the infer path. Callers on another node use NATS.

A missing or invalid token is 401. A valid token whose rule does not allow the call is 403. gRPC uses `UNAUTHENTICATED` and `PERMISSION_DENIED`.

`GET` maps to the verb `get`. `POST` maps to the verb `create`. Infer is resource `models/<name>/infer` with verb `create`. A role may grant one model, or `models/*` and `models/*/infer` for every model in the container. Server live and server ready stay unauthenticated. The full table is in [Authentication](../reference/auth.md).

[Bind a model from Edgelet](edgelet-bind.md) is the catalog that sets `AUTH_MODE=edgelet`. The platform mounts the key. The catalog does not contain it.

## TLS

`TLS_ENABLED` defaults to false, so REST is plain HTTP and gRPC is h2c. `true` and `false` are accepted, ignoring case. Any other value exits the process.

When TLS is on, REST and gRPC present `TLS_CERT` and `TLS_KEY`. Both paths are required, and both files must be readable at startup. A missing file, or only one of the pair, exits the process. `TLS_CA`, when set, must be readable, and both listeners then require a client certificate that chains to that CA. When `TLS_CA` is unset, clients do not present a certificate.

The metrics port stays plain HTTP when TLS is enabled. A TLS terminator in front of the container remains valid while `TLS_ENABLED` is false.

The dashboard has its own `TLS_ENABLED`, `TLS_CERT`, `TLS_KEY`, and `TLS_CA`. It does not read the engine config file. TLS off on the engine does not change the dashboard.

The log never contains the static token, a bearer token, a JWT, the creds file, a private key, or tensor values. A denied call is one `warn` line, message `auth`, with `decision` `unauthenticated` or `forbidden`. Health and metrics are not logged.

The next page is [Choose an image and pass a device](devices.md).
