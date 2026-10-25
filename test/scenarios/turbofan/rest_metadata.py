"""REST server and turbofan-rul metadata.

Requires SERVICEACCOUNT_PATH and INFERENCE_HTTP_URL.
"""

import json
import sys

import common


def main():
    headers = {"Authorization": common.authorization_value()}
    base = common.http_base()
    name = common.model_name()
    calls = [
        ("GET /v2", f"{base}/v2"),
        ("GET /v2/models", f"{base}/v2/models"),
        (f"GET /v2/models/{name}", common.model_url()),
        (f"GET /v2/models/{name}/ready", common.model_url("/ready")),
    ]
    for label, url in calls:
        status, _, body = common.http_call("GET", url, headers=headers)
        print(f"{label}  HTTP {status}")
        text = body.decode("utf-8", errors="replace")
        try:
            print(json.dumps(json.loads(text), indent=2))
        except json.JSONDecodeError:
            print(text)
        print()
        if status != 200:
            raise SystemExit(1)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
