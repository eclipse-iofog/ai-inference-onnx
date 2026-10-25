"""Train the turbofan remaining-life network and write the scenario files.

Reads the four NASA training files under data/, exports model/turbofan-rul.onnx
and model/config.pbtxt, and writes the scaler plus four replay windows under assets/.
The test files are scored after training. They are not part of the fit.
"""

import json
import sys
from pathlib import Path

import numpy as np
import onnx
import onnxruntime as ort
import torch
from torch import nn

ROOT = Path(__file__).resolve().parent
DATA = ROOT / "data"
MODEL_DIR = ROOT / "model"
ASSETS = ROOT / "assets" / "engines"

WINDOW = 30
CHANNELS = 24
CLIP = 125
RUN_AT = 80
STOP_BELOW = 20
SUBSETS = ("FD001", "FD002", "FD003", "FD004")
DEMO = (
    ("fd001-065", "FD001", 65, 128, "run"),
    ("fd003-082", "FD003", 82, 6, "stop"),
    ("fd002-086", "FD002", 86, 194, "run"),
    ("fd004-031", "FD004", 31, 6, "stop"),
)
COLUMNS = (
    ["setting_1", "setting_2", "setting_3"]
    + [f"s{index}" for index in range(1, 22)]
)


class RemainingLife(nn.Module):
    def __init__(self):
        super().__init__()
        self.features = nn.Sequential(
            nn.Conv1d(CHANNELS, 48, kernel_size=3, padding=1),
            nn.ReLU(),
            nn.Conv1d(48, 48, kernel_size=3, padding=1),
            nn.ReLU(),
            nn.MaxPool1d(kernel_size=2),
            nn.Conv1d(48, 64, kernel_size=3, padding=1),
            nn.ReLU(),
            nn.AdaptiveAvgPool1d(1),
        )
        self.head = nn.Linear(64, 1)

    def forward(self, sensors):
        hidden = self.features(sensors.transpose(1, 2)).flatten(1)
        return self.head(hidden)


def main():
    torch.manual_seed(7)
    np.random.seed(7)
    train_units, test_units, test_rul = _load()
    _check_demo(test_units, test_rul)
    fit_units, val_units = _split(train_units)
    mean, std = _scaler(fit_units)
    train_x, train_y = _windows(fit_units, mean, std, stride=2)
    val_x, val_y = _windows(val_units, mean, std, stride=2)
    print(f"train windows: {len(train_y)}")
    print(f"validation windows: {len(val_y)}")
    demo = _demo_batch(test_units, mean, std)
    model = _fit(train_x, train_y, val_x, val_y, demo)
    _score_tests(model, test_units, test_rul, mean, std)
    _export(model)
    _write_assets(test_units, mean, std)
    _check_onnx()
    print("wrote model/turbofan-rul.onnx and assets/engines")


def _load():
    train_units = {}
    test_units = {}
    test_rul = {}
    for subset in SUBSETS:
        train_units[subset] = _read_runs(DATA / f"train_{subset}.txt")
        test_units[subset] = _read_runs(DATA / f"test_{subset}.txt")
        test_rul[subset] = _read_rul(DATA / f"RUL_{subset}.txt")
        if len(test_rul[subset]) != len(test_units[subset]):
            raise SystemExit(
                f"{subset} has {len(test_units[subset])} engines and {len(test_rul[subset])} remaining-life values"
            )
    return train_units, test_units, test_rul


def _read_runs(path):
    units = {}
    with path.open(encoding="utf-8") as handle:
        for line in handle:
            parts = line.split()
            if not parts:
                continue
            values = [float(part) for part in parts]
            if len(values) != 26:
                raise SystemExit(f"{path.name} has {len(values)} columns")
            units.setdefault(int(values[0]), []).append(values[2:26])
    return {unit: np.asarray(rows, dtype=np.float32) for unit, rows in units.items()}


def _read_rul(path):
    return [int(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]


def _check_demo(test_units, test_rul):
    for engine_id, subset, unit, true_rul, _action in DEMO:
        sequence = test_units[subset].get(unit)
        if sequence is None or sequence.shape[0] < WINDOW:
            raise SystemExit(f"{engine_id} does not have {WINDOW} cycles")
        recorded = test_rul[subset][unit - 1]
        if recorded != true_rul:
            raise SystemExit(f"{engine_id} remaining life is {recorded}, expected {true_rul}")


def _split(train_units):
    fit_units = {}
    val_units = {}
    for subset, units in train_units.items():
        fit_units[subset] = {}
        val_units[subset] = {}
        for unit, sequence in units.items():
            bucket = val_units if unit % 10 == 0 else fit_units
            bucket[subset][unit] = sequence
    return fit_units, val_units


def _scaler(units):
    rows = np.concatenate([sequence for subset in units.values() for sequence in subset.values()], axis=0)
    mean = rows.mean(axis=0).astype(np.float32)
    std = rows.std(axis=0).astype(np.float32)
    std = np.where(std < 1e-6, np.float32(1.0), std)
    return mean, std


def _windows(units, mean, std, stride):
    count = 0
    for subset in units.values():
        for sequence in subset.values():
            usable = sequence.shape[0] - WINDOW + 1
            if usable > 0:
                count += (usable + stride - 1) // stride
    windows = np.empty((count, WINDOW, CHANNELS), dtype=np.float32)
    labels = np.empty((count,), dtype=np.float32)
    cursor = 0
    for subset in units.values():
        for sequence in subset.values():
            life = sequence.shape[0]
            for end in range(WINDOW - 1, life, stride):
                windows[cursor] = sequence[end - WINDOW + 1 : end + 1]
                labels[cursor] = min(life - (end + 1), CLIP)
                cursor += 1
    windows = (windows - mean) / std
    return windows, labels


def _demo_batch(test_units, mean, std):
    windows = []
    for _engine_id, subset, unit, _true_rul, _action in DEMO:
        window = test_units[subset][unit][-WINDOW:].astype(np.float32)
        windows.append((window - mean) / std)
    return np.stack(windows).astype(np.float32)


def _fit(train_x, train_y, val_x, val_y, demo):
    device = torch.device("mps" if torch.backends.mps.is_available() else "cpu")
    print(f"device: {device.type}")
    model = RemainingLife().to(device)
    optimizer = torch.optim.Adam(model.parameters(), lr=1e-3, weight_decay=1e-4)
    loss_fn = nn.SmoothL1Loss(reduction="none")
    train_inputs = torch.from_numpy(train_x)
    train_labels = torch.from_numpy(train_y)
    train_weights = torch.ones_like(train_labels)
    train_weights = torch.where(train_labels >= CLIP, 0.35, train_weights)
    train_weights = torch.where(train_labels < 30, 3.0, train_weights)
    val_inputs = torch.from_numpy(val_x).to(device)
    val_labels = torch.from_numpy(val_y).to(device)
    demo_inputs = torch.from_numpy(demo).to(device)
    best_state = None
    best_mae = None
    quiet = 0
    for epoch in range(1, 41):
        model.train()
        order = torch.randperm(len(train_labels))
        total = 0.0
        for start in range(0, len(order), 512):
            batch = order[start : start + 512]
            inputs = train_inputs[batch].to(device)
            labels = train_labels[batch].to(device)
            weights = train_weights[batch].to(device)
            optimizer.zero_grad(set_to_none=True)
            loss = (loss_fn(model(inputs).squeeze(1), labels) * weights).mean()
            loss.backward()
            optimizer.step()
            total += float(loss) * len(batch)
        model.eval()
        with torch.no_grad():
            guess = model(val_inputs).squeeze(1)
            mae = float(torch.mean(torch.abs(guess - val_labels)))
            shown = [int(round(float(value))) for value in model(demo_inputs).reshape(-1)]
        actions = [_decide(value) for value in shown]
        expected = [item[4] for item in DEMO]
        passed = actions == expected
        print(
            f"epoch {epoch:02d}  loss {total / len(train_labels):.3f}  "
            f"validation mae {mae:.2f} cycles  demo {shown}  {'pass' if passed else 'miss'}"
        )
        if passed and (best_mae is None or mae < best_mae - 0.05):
            best_mae = mae
            best_state = {name: value.detach().cpu().clone() for name, value in model.state_dict().items()}
            quiet = 0
        elif best_state is not None:
            quiet += 1
            if quiet >= 8:
                break
    if best_state is None:
        raise SystemExit("no epoch matched the four demo actions")
    model.load_state_dict(best_state)
    model.eval()
    return model.cpu()


def _decide(shown):
    if shown >= RUN_AT:
        return "run"
    if shown < STOP_BELOW:
        return "stop"
    return "plan"


def _score_tests(model, test_units, test_rul, mean, std):
    print("test mean absolute error, clipped at 125 cycles:")
    for subset in SUBSETS:
        errors = []
        for unit, sequence in test_units[subset].items():
            if sequence.shape[0] < WINDOW:
                continue
            predicted = _predict(model, sequence[-WINDOW:], mean, std)
            truth = min(test_rul[subset][unit - 1], CLIP)
            errors.append(abs(predicted - truth))
        print(f"  {subset}  {sum(errors) / len(errors):.1f} cycles  ({len(errors)} engines)")


def _predict(model, window, mean, std):
    scaled = (window.astype(np.float32) - mean) / std
    tensor = torch.from_numpy(scaled).unsqueeze(0)
    with torch.no_grad():
        value = model(tensor).reshape(-1)[0]
    return float(value)


def _export(model):
    MODEL_DIR.mkdir(parents=True, exist_ok=True)
    path = MODEL_DIR / "turbofan-rul.onnx"
    sample = torch.zeros(1, WINDOW, CHANNELS)
    try:
        torch.onnx.export(
            model,
            sample,
            path,
            input_names=["sensors"],
            output_names=["rul"],
            opset_version=17,
            do_constant_folding=True,
            dynamo=False,
        )
    except TypeError:
        torch.onnx.export(
            model,
            sample,
            path,
            input_names=["sensors"],
            output_names=["rul"],
            opset_version=17,
            do_constant_folding=True,
        )
    onnx.checker.check_model(path)
    (MODEL_DIR / "config.pbtxt").write_text(
        'default_model_filename: "turbofan-rul.onnx"\n'
        "max_batch_size: 0\n"
        "version_policy: { latest: { num_versions: 1 } }\n",
        encoding="utf-8",
    )


def _write_assets(test_units, mean, std):
    ASSETS.mkdir(parents=True, exist_ok=True)
    engines = []
    for engine_id, subset, unit, true_rul, action in DEMO:
        window = test_units[subset][unit][-WINDOW:]
        _write_window(ASSETS / f"{engine_id}.f32", window)
        profile = _profile(window)
        engines.append(
            {
                "id": engine_id,
                "subset": subset,
                "unit": unit,
                "true_rul": true_rul,
                "action": action,
                "profile": profile,
                "file": f"{engine_id}.f32",
            }
        )
        print(f"asset {engine_id}  profile {profile}")
    (ROOT / "assets" / "windows.json").write_text(
        json.dumps({"default": "fd004-031", "engines": engines}, indent=2) + "\n",
        encoding="utf-8",
    )
    (ROOT / "assets" / "scaler.json").write_text(
        json.dumps(
            {
                "window": WINDOW,
                "channels": COLUMNS,
                "mean": [float(value) for value in mean],
                "std": [float(value) for value in std],
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )


def _write_window(path, window):
    import array

    values = array.array("f", window.reshape(-1).tolist())
    if sys.byteorder != "little":
        values.byteswap()
    path.write_bytes(values.tobytes())


def _profile(window):
    keys = set()
    for row in window:
        keys.add((round(float(row[0])), round(float(row[1]), 2), round(float(row[2]))))
    if len(keys) == 1 and next(iter(keys))[0] == 0:
        return "sea level"
    return f"{len(keys)} flight conditions"


def _check_onnx():
    import common

    session = ort.InferenceSession(str(MODEL_DIR / "turbofan-rul.onnx"), providers=["CPUExecutionProvider"])
    catalog = json.loads((ROOT / "assets" / "windows.json").read_text(encoding="utf-8"))
    for engine in catalog["engines"]:
        raw = common.load_window(ASSETS / engine["file"])
        profile = common.profile_name(raw)
        if profile != engine["profile"]:
            raise SystemExit(f"{engine['id']} profile {profile}, asset says {engine['profile']}")
        scaled = common.scale(raw)
        blob = common.tensor_blob(scaled)
        result = session.run(None, {"sensors": np.frombuffer(blob, dtype="<f4").reshape(1, WINDOW, CHANNELS)})
        shown = common.shown_rul(float(result[0].reshape(-1)[0]))
        action = common.decide(shown)
        print(f"onnx {engine['id']}  profile {profile}  rul {shown} cycles  action {action}")
        if action != engine["action"]:
            raise SystemExit(f"{engine['id']} action {action}, catalog {engine['action']}")


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
