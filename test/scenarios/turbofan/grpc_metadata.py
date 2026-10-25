"""gRPC server and turbofan-rul metadata.

Requires SERVICEACCOUNT_PATH and INFERENCE_GRPC_TARGET (host:port).
The channel is plaintext h2c.
"""

import sys

import grpc

import common
import open_inference_grpc_pb2 as pb
import open_inference_grpc_pb2_grpc as pb_grpc

CHANNEL_OPTIONS = [
    ("grpc.max_send_message_length", common.GRPC_MESSAGE_BYTES),
    ("grpc.max_receive_message_length", common.GRPC_MESSAGE_BYTES),
]


def main():
    metadata = (("authorization", common.authorization_value()),)
    channel = grpc.insecure_channel(common.grpc_target(), options=CHANNEL_OPTIONS)
    stub = pb_grpc.GRPCInferenceServiceStub(channel)
    try:
        server = stub.ServerMetadata(pb.ServerMetadataRequest(), metadata=metadata, timeout=30)
        print("ServerMetadata")
        print(f"name: {server.name}")
        print(f"version: {server.version}")
        print("extensions: " + ", ".join(server.extensions))
        print()

        model = stub.ModelMetadata(
            pb.ModelMetadataRequest(name=common.model_name()),
            metadata=metadata,
            timeout=30,
        )
        print("ModelMetadata")
        print(f"name: {model.name}")
        print("versions: " + ", ".join(model.versions))
        print(f"platform: {model.platform}")
        for tensor in model.inputs:
            print(f"input {tensor.name} {tensor.datatype} {list(tensor.shape)}")
        for tensor in model.outputs:
            print(f"output {tensor.name} {tensor.datatype} {list(tensor.shape)}")
        print()

        ready = stub.ModelReady(
            pb.ModelReadyRequest(name=common.model_name()),
            metadata=metadata,
            timeout=30,
        )
        print(f"ModelReady ready: {ready.ready}")
        if not ready.ready:
            raise SystemExit(1)
    except grpc.RpcError as err:
        print(f"{err.code().name}: {err.details()}", file=sys.stderr)
        raise SystemExit(1) from err
    finally:
        channel.close()


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
