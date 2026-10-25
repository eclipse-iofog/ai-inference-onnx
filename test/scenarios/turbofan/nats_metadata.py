"""NATS server and turbofan-rul metadata.

Requires NATS_SERVER_URL and NATS_CREDS_PATH. NATS does not use the service-account token.
"""

import asyncio
import json
import sys

import common


async def main():
    name = common.model_name()
    calls = [
        ("inference.v2.server.metadata", b""),
        ("inference.v2.models", b""),
        (f"inference.v2.models.{name}.metadata", b""),
        (f"inference.v2.models.{name}.ready", b""),
    ]
    nc = await common.nats_connect()
    try:
        for subject, payload in calls:
            headers, body = await common.nats_round_trip(nc, subject, payload, None, timeout=30)
            status = common.nats_status(headers)
            print(f"{subject}  status {status}")
            text = body.decode("utf-8", errors="replace")
            if text:
                try:
                    print(json.dumps(json.loads(text), indent=2))
                except json.JSONDecodeError:
                    print(text)
            else:
                print("(empty)")
            print()
            if status != 200:
                raise SystemExit(1)
    finally:
        await nc.close()


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        sys.exit(130)
