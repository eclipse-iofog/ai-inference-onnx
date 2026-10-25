# syntax=docker/dockerfile:1

FROM python:3.12-slim-bookworm

RUN groupadd --gid 10001 scenarios \
    && useradd --uid 10001 --gid 10001 --create-home --shell /usr/sbin/nologin scenarios

WORKDIR /opt/scenarios

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY .build/proto/open_inference_grpc.proto /tmp/proto/open_inference_grpc.proto
RUN python -m grpc_tools.protoc \
        -I/tmp/proto \
        --python_out=/opt/scenarios \
        --grpc_python_out=/opt/scenarios \
        /tmp/proto/open_inference_grpc.proto \
    && rm -rf /tmp/proto

COPY common.py rest_metadata.py rest_infer.py grpc_metadata.py grpc_infer.py nats_metadata.py nats_infer_rendezvous.py ./
COPY assets ./assets

RUN chown -R 10001:10001 /opt/scenarios

ENV SERVICEACCOUNT_PATH=/var/run/secrets/edgelet.iofog.org/serviceaccount \
    MODEL_NAME=turbofan-rul \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONPATH=/opt/scenarios

USER 10001:10001

CMD ["/bin/sh", "-c", "trap true INT TERM; sleep infinity & wait"]
