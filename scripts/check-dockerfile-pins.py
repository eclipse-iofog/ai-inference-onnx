#!/usr/bin/env python3
import pathlib
import re
import sys

PINNED = r"[a-z0-9.\-/:]+:[A-Za-z0-9._\-]+@sha256:[0-9a-f]{64}"
FROM_RE = re.compile(r"^FROM\s+(?:--platform=\S+\s+)?(\S+)(?:\s+AS\s+(\S+))?\s*$", re.I)
COPY_FROM = re.compile(r"^COPY\s+.*--from=(\S+)", re.I)
FINAL_BASES = (
    "registry.access.redhat.com/ubi9/ubi-micro:9.8@",
    "gcr.io/distroless/cc-debian13:nonroot@",
)
FORBIDDEN_IN_FINAL = re.compile(r"^RUN\b|\b(apt-get|apt|microdnf|dnf|yum|apk|rpm)\b", re.I)
LOCK_LINE = re.compile(
    r"^[a-z0-9]+ 1\.31\.0-[a-z0-9]+@sha256:[0-9a-f]{64}$"
)
fail = []

def check(path, runtime):
    stages, names = [], set()
    for n, raw in enumerate(path.read_text().splitlines(), 1):
        line = raw.strip()
        if line.startswith("#") and "sha256:" in line:
            fail.append(f"{path}:{n}: pin comment line")
        m = FROM_RE.match(line)
        if m:
            ref, name = m.group(1), (m.group(2) or "").lower()
            stages.append({"ref": ref, "name": name, "body": []})
            if name:
                names.add(name)
            alias = re.fullmatch(r"([a-z0-9\-]+-)\$\{?targetarch\}?", ref.lower())
            if ref == "scratch" or ref.lower() in names or (
                alias and any(x.startswith(alias.group(1)) for x in names)
            ):
                continue
            if not re.fullmatch(PINNED, ref):
                fail.append(f"{path}:{n}: not name:tag@sha256: {ref}")
            continue
        if stages:
            stages[-1]["body"].append((n, line))
        c = COPY_FROM.match(line)
        if c:
            src = c.group(1)
            if src.lower() not in names and not re.fullmatch(PINNED, src):
                fail.append(f"{path}:{n}: unpinned COPY --from={src}")
    if not stages:
        fail.append(f"{path}: no FROM")
        return
    if runtime:
        last = stages[-1]
        if not (last["ref"] == "scratch" and last["name"] == "runtime"):
            fail.append(f"{path}: last stage is not FROM scratch AS runtime")
        return
    finals = [s for s in stages if s["ref"].startswith(FINAL_BASES)]
    if not finals:
        fail.append(f"{path}: no final stage on ubi-micro:9.8 or distroless cc-debian13:nonroot")
    for s in stages:
        if s["name"].startswith("final") and s not in finals:
            fail.append(f"{path}: final stage {s['name']} on {s['ref']}")
    for s in finals:
        if not any(l == "USER 10001:10001" for _, l in s["body"]):
            fail.append(f"{path}: final stage {s['name']} lacks USER 10001:10001")
        for n, l in s["body"]:
            if FORBIDDEN_IN_FINAL.search(l):
                fail.append(f"{path}:{n}: forbidden in final stage: {l}")

engine = sorted(pathlib.Path("build").glob("Dockerfile*"))
runtime = sorted(pathlib.Path("build/onnxruntime").glob("Dockerfile*"))
if not engine and not runtime:
    sys.exit("no Dockerfiles")
for p in engine:
    check(p, False)
for p in runtime:
    check(p, True)
lock = pathlib.Path("build/onnxruntime/runtime-images.txt")
if lock.exists():
    seen = set()
    for n, raw in enumerate(lock.read_text().splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if "/" in line or "ghcr.io" in line.lower():
            fail.append(f"{lock}:{n}: registry host is not allowed")
            continue
        if not LOCK_LINE.match(line):
            fail.append(f"{lock}:{n}: malformed line")
            continue
        variant = line.split()[0]
        if not line.split()[1].split("@")[0].endswith("-" + variant):
            fail.append(f"{lock}:{n}: tag does not match {variant}")
        if variant in seen:
            fail.append(f"{lock}:{n}: duplicate {variant}")
        seen.add(variant)
for f in fail:
    print(f)
sys.exit(1 if fail else 0)
