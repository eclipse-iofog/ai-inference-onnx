# Authentication

`authMode` is required. If neither the environment nor the config file sets it, the process exits. The value is `disabled`, `edgelet`, or `static`. It applies to REST and gRPC.

Health is open in every mode: `GET /v2/health/live` and `GET /v2/health/ready` on REST, and `ServerLive` and `ServerReady` on gRPC. `GET /metrics` on the metrics port is open. A scraper has no inference credential, and the model names on that port are operational metadata.

## `disabled`

`disabled` performs no check. Use it for `docker run`, `podman run`, and CI.

## `static`

`static` requires `Authorization: Bearer` equal to `authStaticToken`. gRPC takes the same value in metadata. The comparison is constant time. The value is at least 32 characters. If it is missing or shorter, the process exits. The value is never logged. Changing it means a restart.

The token is not a JWT and has no per-model rules. Presenting it allows every protected call.

## `edgelet`

`edgelet` requires an Edgelet service-account JWT on REST and gRPC. The engine watches `/var/run/secrets/edgelet.iofog.org/serviceaccount/edgelet.jwk` and swaps the public key in memory when the file changes. A restart is not required. The key is a public Ed25519 JWK (`kty` `OKP`, `crv` `Ed25519`, `alg` `EdDSA`). The engine reads that file. It does not parse its own token to obtain the node public key.

If `edgelet.jwk` is missing or unreadable at startup, the process exits. After startup, a missing or unreadable key fails new REST and gRPC calls until a valid key is loaded again.

REST takes `Authorization: Bearer`. gRPC takes the same value in metadata. The check order is:

1. The signature matches the current `edgelet.jwk`, algorithm EdDSA.
2. `tokenUse` is `serviceaccount`, and `iat`, `nbf`, `exp`, and `iss` are valid. The issuer is `https://edgelet.default.svc.bridge.local`.
3. `rulesByGroup` allows the operation under this process's API group.

The group is chosen once at startup. The checker reads that group.

1. `apiGroupPrefix`, or `API_GROUP_PREFIX`, when set. The group is `{prefix}.inference.iofog.org/v2`. The environment overrides the file.
2. `EDGELET_APPLICATION_NAME` and `EDGELET_MICROSERVICE_NAME`, when both are set and the prefix is unset. The group is `{application}.{microservice}.inference.iofog.org/v2`.
3. `inference.iofog.org/v2`, when the prefix is unset and either platform name is missing.

The prefix, and each platform name used in the group, must be DNS labels: lowercase letters, digits, and hyphens, separated by dots. A label starts and ends with a letter or digit, and it is at most 63 characters. The name in front of `/v2` is at most 253 characters. A prefix that is not made of DNS labels exits the process in every auth mode. `edgelet` also exits when both platform names are set, the prefix is unset, and either name is not a DNS label. `static` and `disabled` do not read the group. In those modes an unusable platform name leaves the group at `inference.iofog.org/v2`.

Engines that should accept the same role set the same prefix. The application and microservice names still identify the process in metrics and on the dashboard. `start` logs the group as `api_group`.

The `aud` claim on these tokens is the Edgelet bridge, `https://edgelet.default.svc.bridge.local`. The engine accepts that audience. The signature shows the token was minted by this node. The API group shows the caller may call this process.

A missing or invalid token is 401. A valid token whose rule does not allow the call is 403. gRPC uses the matching status codes. The body is `{"error":"..."}` and does not echo claims.

Revocation is the token `exp` window. The engine does not call Edgelet on the infer path. Each node has its own signing key, so a token from another node fails verification. Callers on another node use NATS.

A call is denied unless a rule under the chosen group allows it. `GET` maps to the verb `get`, and `POST` maps to the verb `create`.

| Call | Resource | Verb |
|---|---|---|
| Server metadata | `server` | `get` |
| `GET /v2/models` | `models` | `get` |
| Model metadata and model ready | `models/<name>` | `get` |
| Infer | `models/<name>/infer` | `create` |
| `POST /v2/repository/index` | `repository/index` | `get` |

`POST /v2/repository/index` stays a POST because the ready filter is in the body. The permission is `get`. A role may grant one model, or `models/*` and `models/*/infer` for every model in the container.

Server live and server ready stay unauthenticated. Model metadata, model ready, and infer require a matching rule. In `static` mode the bearer token stands in for the rule.

## NATS

NATS does not check the service-account JWT. Account and user rules on the NATS server authenticate the connection. A queue group balances load across engines. It is not an authentication check.
