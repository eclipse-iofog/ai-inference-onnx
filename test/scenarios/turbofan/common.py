"""Shared engine-window, token, and remaining-life helpers for the scenario scripts."""

import array
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent
ASSETS = ROOT / "assets"
ENGINES = ASSETS / "engines"
WINDOWS_PATH = ASSETS / "windows.json"
SCALER_PATH = ASSETS / "scaler.json"

WINDOW = 30
CHANNELS = 24
INPUT_NAME = "sensors"
OUTPUT_NAME = "rul"
RUN_AT = 80
STOP_BELOW = 20
TENSOR_BYTES = WINDOW * CHANNELS * 4
GRPC_MESSAGE_BYTES = 32 * 1024 * 1024


def model_name():
    name = os.environ.get("MODEL_NAME", "turbofan-rul").strip() or "turbofan-rul"
    if "/" in name or "*" in name or ">" in name:
        raise SystemExit("MODEL_NAME cannot contain /, *, or >")
    return name


def bearer_token():
    directory = os.environ.get("SERVICEACCOUNT_PATH", "").strip()
    if not directory:
        raise SystemExit("SERVICEACCOUNT_PATH is required")
    path = Path(directory) / "token"
    try:
        value = path.read_text(encoding="utf-8").strip()
    except OSError as err:
        raise SystemExit(f"cannot read {path}: {err}") from err
    if not value:
        raise SystemExit(f"{path} is empty")
    return value


def authorization_value():
    return f"Bearer {bearer_token()}"


def http_base():
    value = os.environ.get("INFERENCE_HTTP_URL", "").strip().rstrip("/")
    if not value:
        raise SystemExit("INFERENCE_HTTP_URL is required")
    if not value.startswith(("http://", "https://")):
        raise SystemExit("INFERENCE_HTTP_URL must start with http:// or https://")
    return value


def grpc_target():
    value = os.environ.get("INFERENCE_GRPC_TARGET", "").strip()
    if not value:
        raise SystemExit("INFERENCE_GRPC_TARGET is required")
    if "://" in value:
        raise SystemExit("INFERENCE_GRPC_TARGET is host:port, for example engine:8001")
    return value


def catalog():
    try:
        document = json.loads(WINDOWS_PATH.read_text(encoding="utf-8"))
    except OSError as err:
        raise SystemExit(f"cannot read {WINDOWS_PATH}: {err}") from err
    engines = document.get("engines") or []
    if not engines:
        raise SystemExit(f"{WINDOWS_PATH} has no engines")
    return document


def engine_by_id(engine_id):
    for engine in catalog()["engines"]:
        if engine["id"] == engine_id:
            return engine
    return None


def selected_engines(argv):
    program = Path(argv[0]).name
    args = argv[1:]
    if len(args) > 1 or (args and args[0].startswith("-") and args[0] != "--all"):
        raise SystemExit(f"usage: python3 {program} [engine|--all]")
    document = catalog()
    engines = document["engines"]
    if args == ["--all"]:
        return engines, True
    engine_id = args[0] if args else document.get("default") or engines[0]["id"]
    chosen = engine_by_id(engine_id)
    if chosen is None:
        raise SystemExit(f"engine not found: {engine_id}")
    return [chosen], False


def load_window(path):
    try:
        blob = path.read_bytes()
    except OSError as err:
        raise SystemExit(f"cannot read {path}: {err}") from err
    if len(blob) != TENSOR_BYTES:
        raise SystemExit(f"{path.name} is {len(blob)} bytes, expected {TENSOR_BYTES}")
    values = array.array("f")
    values.frombytes(blob)
    if sys.byteorder != "little":
        values.byteswap()
    if len(values) != WINDOW * CHANNELS:
        raise SystemExit(f"{path.name} has {len(values)} values, expected {WINDOW * CHANNELS}")
    return values


def scaler():
    try:
        document = json.loads(SCALER_PATH.read_text(encoding="utf-8"))
    except OSError as err:
        raise SystemExit(f"cannot read {SCALER_PATH}: {err}") from err
    mean = document.get("mean") or []
    std = document.get("std") or []
    if len(mean) != CHANNELS or len(std) != CHANNELS:
        raise SystemExit(f"{SCALER_PATH.name} must list {CHANNELS} means and {CHANNELS} standard deviations")
    return mean, std


def scale(values):
    mean, std = scaler()
    scaled = array.array("f", values)
    for index, value in enumerate(values):
        channel = index % CHANNELS
        scaled[index] = (value - float(mean[channel])) / float(std[channel])
    return scaled


def tensor_blob(values):
    out = array.array("f", values)
    if sys.byteorder != "little":
        out.byteswap()
    blob = out.tobytes()
    if len(blob) != TENSOR_BYTES:
        raise SystemExit(f"tensor is {len(blob)} bytes, expected {TENSOR_BYTES}")
    return blob


def profile_name(values):
    keys = set()
    for cycle in range(WINDOW):
        base = cycle * CHANNELS
        keys.add((round(values[base]), round(values[base + 1], 2), round(values[base + 2])))
    if len(keys) == 1 and next(iter(keys))[0] == 0:
        return "sea level"
    return f"{len(keys)} flight conditions"


def shown_rul(value):
    return int(round(float(value)))


def decide(shown):
    if shown >= RUN_AT:
        return "run"
    if shown < STOP_BELOW:
        return "stop"
    return "plan"


def prepare(engine):
    raw = load_window(ENGINES / engine["file"])
    return profile_name(raw), tensor_blob(scale(raw))


def http_call(method, url, body=None, headers=None, timeout=30):
    request = urllib.request.Request(url, data=body, method=method, headers=headers or {})
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.status, response.headers, response.read()
    except urllib.error.HTTPError as err:
        return err.code, err.headers, err.read()


def model_url(suffix=""):
    name = urllib.parse.quote(model_name(), safe="")
    return f"{http_base()}/v2/models/{name}{suffix}"


def infer_header(request_id, tensor_length):
    document = {
        "id": request_id,
        "parameters": {"binary_data_output": True},
        "inputs": [
            {
                "name": INPUT_NAME,
                "datatype": "FP32",
                "shape": [1, WINDOW, CHANNELS],
                "parameters": {"binary_data_size": tensor_length},
            }
        ],
    }
    return json.dumps(document, separators=(",", ":")).encode()


def header_value(headers, name):
    if not headers:
        return ""
    direct = headers.get(name)
    if direct:
        return direct
    lowered = name.lower()
    for key, value in headers.items():
        if str(key).lower() == lowered and value:
            return value
    return ""


def output_values(document, body, header_length):
    outputs = document.get("outputs") or []
    chosen = next((item for item in outputs if item.get("name") == OUTPUT_NAME), None)
    if chosen is None:
        names = [item.get("name") for item in outputs]
        raise SystemExit(f"response has no {OUTPUT_NAME}: {names}")
    shape = [int(dim) for dim in chosen.get("shape") or []]
    if header_length is None:
        data = chosen.get("data")
        if not isinstance(data, list):
            raise SystemExit(f"{OUTPUT_NAME} has no data")
        return shape, array.array("f", (float(value) for value in data))
    blob = body[header_length:]
    offset = 0
    for item in outputs:
        count = 1
        for dim in item.get("shape") or []:
            count *= int(dim)
        size = count * 4
        chunk = blob[offset : offset + size]
        offset += size
        if item.get("name") != OUTPUT_NAME:
            continue
        if len(chunk) != size:
            raise SystemExit(f"{OUTPUT_NAME} binary length is {len(chunk)}, expected {size}")
        values = array.array("f")
        values.frombytes(chunk)
        if sys.byteorder != "little":
            values.byteswap()
        return shape, values
    raise SystemExit(f"binary suffix ended before {OUTPUT_NAME}")


def decode_infer(body, header_length):
    if header_length is None:
        document = json.loads(body)
    else:
        if header_length < 0 or header_length > len(body):
            raise SystemExit(f"Inference-Header-Content-Length {header_length} is outside the body")
        document = json.loads(body[:header_length])
    shape, values = output_values(document, body, header_length)
    return document, shape, values


def rul_value(shape, values):
    count = 1
    for dim in shape:
        count *= dim
    if count != 1 or len(values) != 1:
        raise SystemExit(f"expected one remaining-life value, got shape {shape}")
    return shown_rul(values[0])


def report_rul(engine, model, version, profile, rul):
    print(f"engine: {engine['id']}")
    print(f"model: {model}")
    print(f"version: {version}")
    print(f"profile: {profile}")
    print(f"rul_cycles: {rul}")
    print(f"action: {decide(rul)}")
    print(f"true_rul_cycles: {engine['true_rul']}")
    if decide(rul) != engine["action"]:
        raise SystemExit(f"{engine['id']} action {decide(rul)}, catalog {engine['action']}")


def rul_line(engine, profile, rul):
    action = decide(rul)
    ok = action == engine["action"]
    return ok, f"{engine['id']}  profile {profile}  rul {rul} cycles  action {action}"


def finish_walk(matched, total):
    print(f"actions: {matched}/{total}")
    if matched != total:
        raise SystemExit(1)


def walk_error(err):
    if not isinstance(err, SystemExit):
        return str(err)
    code = err.code
    if code in (0, None) or isinstance(code, int):
        raise err
    return str(code)


def status_error(status, body):
    text = body.decode("utf-8", errors="replace").strip()
    if text:
        return f"status {status}: {text}"
    return f"status {status}"


def slice_bytes():
    raw = os.environ.get("NATS_SLICE_BYTES", "524288").strip() or "524288"
    try:
        value = int(raw)
    except ValueError:
        raise SystemExit("NATS_SLICE_BYTES must be a positive integer") from None
    if value < 1:
        raise SystemExit("NATS_SLICE_BYTES must be a positive integer")
    return value


def nats_settings():
    url = os.environ.get("NATS_SERVER_URL", "").strip()
    creds = os.environ.get("NATS_CREDS_PATH", "").strip()
    if not url or not creds:
        raise SystemExit("NATS_SERVER_URL and NATS_CREDS_PATH are required")
    return url, creds


async def nats_connect():
    import nats

    url, creds = nats_settings()
    nc = await nats.connect(url, user_credentials=creds, name="turbofan-scenarios", connect_timeout=10)
    return nc


async def nats_round_trip(nc, subject, payload, headers, timeout):
    import nats.errors

    inbox = nc.new_inbox()
    sub = await nc.subscribe(inbox)
    try:
        await nc.publish(subject, payload, reply=inbox, headers=headers)
        try:
            first = await sub.next_msg(timeout=timeout)
        except nats.errors.TimeoutError:
            raise SystemExit(f"no reply on {subject} within {timeout:.0f}s") from None
        parts = [first.data or b""]
        reply_headers = first.headers or {}
        while True:
            try:
                nxt = await sub.next_msg(timeout=5)
            except nats.errors.TimeoutError:
                break
            if not nxt.data and not nxt.headers:
                break
            parts.append(nxt.data or b"")
        return reply_headers, b"".join(parts)
    finally:
        await sub.unsubscribe()


def nats_status(headers):
    raw = header_value(headers, "Inference-Status")
    if not raw:
        raise SystemExit("reply has no Inference-Status")
    try:
        return int(raw)
    except ValueError:
        raise SystemExit(f"Inference-Status is {raw!r}") from None


def fail_status(status, body):
    print(status_error(status, body), file=sys.stderr)
    raise SystemExit(1)
