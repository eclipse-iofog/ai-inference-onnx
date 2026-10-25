"""REST infer for one wafer image, or every image under assets/wafers.

Requires SERVICEACCOUNT_PATH and INFERENCE_HTTP_URL.
The default image is assets/wafers/wafer-scratch-001.jpg.

    python3 rest_infer.py
    python3 rest_infer.py /opt/scenarios/assets/wafers/wafer-donut-001.jpg
    python3 rest_infer.py --all
"""

import sys

import common


def main():
    images, walk = common.selected_images(sys.argv)
    if not walk:
        _report(images[0])
        return
    detected = 0
    for image in images:
        try:
            ok, line = _line(image)
        except (Exception, SystemExit) as err:
            print(f"{image.name}  error: {common.walk_error(err)}")
            continue
        print(line)
        detected += int(ok)
    common.finish_walk(detected, len(images))


def _report(image):
    document, shape, values = _infer(image)
    common.report_detection(
        image.name,
        document.get("model_name", ""),
        document.get("model_version", ""),
        shape,
        values,
    )


def _line(image):
    document, shape, values = _infer(image)
    return common.detection_line(image.name, shape, values)


def _infer(image):
    tensor = common.image_tensor(image)
    header = common.infer_header(image.stem, len(tensor))
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
    return document, shape, values


if __name__ == "__main__":
    try:
        main()
    except RuntimeError as err:
        print(err, file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        sys.exit(130)
