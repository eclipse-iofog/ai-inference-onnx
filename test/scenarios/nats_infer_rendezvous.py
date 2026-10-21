"""NATS rendezvous infer for one wafer image, or every image under assets/wafers.

Requires NATS_SERVER_URL and NATS_CREDS_PATH. NATS does not use the service-account token.
The tensor is uploaded in slices of NATS_SLICE_BYTES (default 512 KiB), capped by the server max payload.
The default image is assets/wafers/wafer-scratch-001.jpg.

    python3 nats_infer_rendezvous.py
    python3 nats_infer_rendezvous.py /opt/scenarios/assets/wafers/wafer-donut-001.jpg
    python3 nats_infer_rendezvous.py --all
"""

import asyncio
import sys

import common


async def main():
    images, walk = common.selected_images(sys.argv)
    subject = f"inference.v2.models.{common.model_name()}.infer"
    nc = await common.nats_connect()
    try:
        room = nc.max_payload - 512
        if room < 1:
            raise SystemExit(f"NATS max payload {nc.max_payload} is too small for a slice")
        step = min(common.slice_bytes(), room)
        if not walk:
            await _report(nc, subject, images[0], step)
            return
        detected = 0
        for image in images:
            print(image.name)
            try:
                ok, line = await _line(nc, subject, image, step)
            except (Exception, SystemExit) as err:
                print(f"{image.name}  error: {common.walk_error(err)}")
                continue
            print(line)
            detected += int(ok)
        common.finish_walk(detected, len(images))
    finally:
        await nc.close()


async def _report(nc, subject, image, step):
    document, shape, values = await _infer(nc, subject, image, step, announce=True)
    common.report_detection(
        image.name,
        document.get("model_name", ""),
        document.get("model_version", ""),
        shape,
        values,
    )


async def _line(nc, subject, image, step):
    document, shape, values = await _infer(nc, subject, image, step, announce=False)
    return common.detection_line(image.name, shape, values)


async def _infer(nc, subject, image, step, announce):
    tensor = common.image_tensor(image)
    header = common.infer_header(image.stem, len(tensor))
    accepted, upload_id = await _accept(nc, subject, header, len(tensor))
    if announce:
        print(f"accepted {accepted}")
    body_headers, body = await _upload(nc, accepted, upload_id, tensor, step)
    status = common.nats_status(body_headers)
    if status != 200:
        raise RuntimeError(common.status_error(status, body))
    length = common.header_value(body_headers, "Inference-Header-Content-Length")
    header_length = int(length) if length else None
    return common.decode_infer(body, header_length)


async def _accept(nc, subject, header, suffix_length):
    headers, body = await common.nats_round_trip(
        nc,
        subject,
        header,
        {
            "Inference-Transfer": "rendezvous",
            "Inference-Suffix-Length": str(suffix_length),
        },
        timeout=30,
    )
    status = common.nats_status(headers)
    if status != 200:
        raise RuntimeError(common.status_error(status, body))
    accepted = common.header_value(headers, "Inference-Accept")
    prefix = "inference.xfer."
    if not accepted.startswith(prefix) or accepted == prefix:
        raise SystemExit(f"Inference-Accept is {accepted!r}")
    return accepted, accepted[len(prefix) :]


async def _upload(nc, subject, upload_id, tensor, step):
    count = (len(tensor) + step - 1) // step
    headers = {"Inference-Id": upload_id}
    reply_headers = None
    reply_body = b""
    offset = 0
    for number in range(1, count + 1):
        chunk = tensor[offset : offset + step]
        offset += len(chunk)
        last = offset == len(tensor)
        print(f"slice {number}/{count}, {len(chunk)} bytes")
        reply_headers, reply_body = await common.nats_round_trip(
            nc,
            subject,
            chunk,
            headers,
            timeout=120 if last else 10,
        )
        status = common.nats_status(reply_headers)
        if status != 200:
            raise RuntimeError(common.status_error(status, reply_body))
        if not last and reply_body:
            raise SystemExit(f"slice {number} returned a body before the upload finished")
    return reply_headers, reply_body


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except RuntimeError as err:
        print(err, file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        sys.exit(130)
