"""REST infer for one engine window, or every window under assets/engines.

Requires SERVICEACCOUNT_PATH and INFERENCE_HTTP_URL.
The default engine is fd004-031.

    python3 rest_infer.py
    python3 rest_infer.py fd001-065
    python3 rest_infer.py --all
"""

import sys

import common


def main():
    engines, walk = common.selected_engines(sys.argv)
    if not walk:
        _report(engines[0])
        return
    matched = 0
    for engine in engines:
        try:
            ok, line = _line(engine)
        except (Exception, SystemExit) as err:
            print(f"{engine['id']}  error: {common.walk_error(err)}")
            continue
        print(line)
        matched += int(ok)
    common.finish_walk(matched, len(engines))


def _report(engine):
    document, profile, rul = _infer(engine)
    common.report_rul(
        engine,
        document.get("model_name", ""),
        document.get("model_version", ""),
        profile,
        rul,
    )


def _line(engine):
    _document, profile, rul = _infer(engine)
    return common.rul_line(engine, profile, rul)


def _infer(engine):
    profile, tensor = common.prepare(engine)
    header = common.infer_header(engine["id"], len(tensor))
    status, headers, body = common.http_call(
        "POST",
        common.model_url("/infer"),
        body=header + tensor,
        headers={
            "Authorization": common.authorization_value(),
            "Content-Type": "application/octet-stream",
            "Inference-Header-Content-Length": str(len(header)),
        },
        timeout=120,
    )
    if status != 200:
        raise RuntimeError(common.status_error(status, body))
    length = common.header_value(headers, "Inference-Header-Content-Length")
    header_length = int(length) if length else None
    document, shape, values = common.decode_infer(body, header_length)
    return document, profile, common.rul_value(shape, values)


if __name__ == "__main__":
    try:
        main()
    except RuntimeError as err:
        print(err, file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        sys.exit(130)
