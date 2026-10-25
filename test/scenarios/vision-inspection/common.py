"""Shared wafer tensor, token, and detection helpers for the scenario scripts."""

import array
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent
WAFERS = ROOT / "assets" / "wafers"
DEFAULT_IMAGE = WAFERS / "wafer-scratch-001.jpg"
CLASSES_PATH = ROOT / "assets" / "wafer-classes.json"

SIDE = 640
INPUT_NAME = "images"
OUTPUT_NAME = "output0"
NUM_CLASSES = 6
FEATURES = 4 + NUM_CLASSES
CONFIDENCE = 0.25
NMS_IOU = 0.45
TENSOR_BYTES = 1 * 3 * SIDE * SIDE * 4
GRPC_MESSAGE_BYTES = 32 * 1024 * 1024


def model_name():
    name = os.environ.get("MODEL_NAME", "wafer-defect").strip() or "wafer-defect"
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


def selected_images(argv):
    program = Path(argv[0]).name
    args = argv[1:]
    if len(args) > 1 or (args and args[0].startswith("-") and args[0] != "--all"):
        raise SystemExit(f"usage: python3 {program} [image|--all]")
    if args == ["--all"]:
        images = sorted(WAFERS.glob("*.jpg"))
        if not images:
            raise SystemExit(f"no images in {WAFERS}")
        return images, True
    path = Path(args[0]) if args else DEFAULT_IMAGE
    if not path.is_file():
        raise SystemExit(f"image not found: {path}")
    return [path], False


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


def image_tensor(path):
    from PIL import Image

    with Image.open(path) as image:
        rgb = image.convert("RGB")
        raw = rgb.tobytes()
        width, height = rgb.size
    if width < 1 or height < 1:
        raise SystemExit(f"image {path} is empty")
    plane = SIDE * SIDE
    red = array.array("f", (0.0,)) * plane
    green = array.array("f", (0.0,)) * plane
    blue = array.array("f", (0.0,)) * plane
    index = 0
    for y in range(SIDE):
        sy = y * height // SIDE
        row = sy * width
        for x in range(SIDE):
            sx = x * width // SIDE
            pos = (row + sx) * 3
            red[index] = raw[pos] / 255.0
            green[index] = raw[pos + 1] / 255.0
            blue[index] = raw[pos + 2] / 255.0
            index += 1
    values = red + green + blue
    if sys.byteorder != "little":
        values.byteswap()
    blob = values.tobytes()
    if len(blob) != TENSOR_BYTES:
        raise SystemExit(f"tensor is {len(blob)} bytes, expected {TENSOR_BYTES}")
    return blob


def infer_header(request_id, tensor_length):
    document = {
        "id": request_id,
        "parameters": {"binary_data_output": True},
        "inputs": [
            {
                "name": INPUT_NAME,
                "datatype": "FP32",
                "shape": [1, 3, SIDE, SIDE],
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


def best_detection(shape, values):
    if len(shape) != 3 or shape[0] != 1 or shape[1] != FEATURES:
        raise SystemExit(f"expected output shape [1, {FEATURES}, N], got {shape}")
    num_preds = shape[2]
    if len(values) < FEATURES * num_preds:
        num_preds = len(values) // FEATURES
    boxes = []
    for i in range(num_preds):
        cx = float(values[0 * num_preds + i])
        cy = float(values[1 * num_preds + i])
        width = float(values[2 * num_preds + i])
        height = float(values[3 * num_preds + i])
        class_index = 0
        score = float(values[4 * num_preds + i])
        for cls in range(1, NUM_CLASSES):
            candidate = float(values[(4 + cls) * num_preds + i])
            if candidate > score:
                score = candidate
                class_index = cls
        if score < CONFIDENCE:
            continue
        boxes.append(
            {
                "x1": cx - width / 2,
                "y1": cy - height / 2,
                "x2": cx + width / 2,
                "y2": cy + height / 2,
                "class_index": class_index,
                "confidence": score,
            }
        )
    kept = _suppress(boxes)
    if not kept:
        return None
    return kept[0]


def report_detection(image, model, version, shape, values):
    found = best_detection(shape, values)
    print(f"image: {image}")
    print(f"model: {model}")
    print(f"version: {version}")
    if found is None:
        print(f"no detection above {CONFIDENCE}")
        raise SystemExit(1)
    defect_class, label, risk = _label(found)
    print(f"defect_class: {defect_class}")
    print(f"label: {label}")
    print(f"confidence: {found['confidence']:.4f}")
    print(f"yield_risk: {risk}")
    print(f"box: {_box(found)}")


def detection_line(image_name, shape, values):
    found = best_detection(shape, values)
    if found is None:
        return False, f"{image_name}  no detection above {CONFIDENCE}"
    defect_class, label, risk = _label(found)
    return True, (
        f"{image_name}  {defect_class}  {label}  {found['confidence']:.4f}  {risk}  {_box(found)}"
    )


def finish_walk(detected, total):
    print(f"detections: {detected}/{total}")
    if detected != total:
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
    nc = await nats.connect(url, user_credentials=creds, name="wafer-scenarios", connect_timeout=10)
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


def _label(found):
    item = _classes().get(found["class_index"])
    if item is None:
        defect_class, label, base_risk = "unknown", "Unknown", "low"
    else:
        defect_class = item["id"]
        label = item["label"]
        base_risk = item.get("yieldRisk", "low")
    risk = base_risk if found["confidence"] >= 0.5 and base_risk in ("high", "medium", "low") else "low"
    return defect_class, label, risk


def _box(found):
    return f"{found['x1']:.1f} {found['y1']:.1f} {found['x2']:.1f} {found['y2']:.1f}"


def _classes():
    try:
        document = json.loads(CLASSES_PATH.read_text(encoding="utf-8"))
    except OSError as err:
        raise SystemExit(f"cannot read {CLASSES_PATH}: {err}") from err
    by_index = {}
    for item in document.get("classes") or []:
        by_index[int(item["index"])] = item
    return by_index


def _suppress(boxes):
    boxes.sort(key=lambda box: box["confidence"], reverse=True)
    suppressed = [False] * len(boxes)
    kept = []
    for i, box in enumerate(boxes):
        if suppressed[i]:
            continue
        kept.append(box)
        for j in range(i + 1, len(boxes)):
            other = boxes[j]
            if suppressed[j] or box["class_index"] != other["class_index"]:
                continue
            if _iou(box, other) > NMS_IOU:
                suppressed[j] = True
    return kept


def _iou(a, b):
    x1 = max(a["x1"], b["x1"])
    y1 = max(a["y1"], b["y1"])
    x2 = min(a["x2"], b["x2"])
    y2 = min(a["y2"], b["y2"])
    inter = max(0.0, x2 - x1) * max(0.0, y2 - y1)
    if inter == 0:
        return 0.0
    area_a = max(0.0, a["x2"] - a["x1"]) * max(0.0, a["y2"] - a["y1"])
    area_b = max(0.0, b["x2"] - b["x1"]) * max(0.0, b["y2"] - b["y1"])
    union = area_a + area_b - inter
    if union <= 0:
        return 0.0
    return inter / union
