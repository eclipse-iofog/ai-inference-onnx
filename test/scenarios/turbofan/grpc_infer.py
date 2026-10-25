"""gRPC infer for one engine window, or every window under assets/engines.

Requires SERVICEACCOUNT_PATH and INFERENCE_GRPC_TARGET (host:port).
The channel is plaintext h2c. The default engine is fd004-031.

    python3 grpc_infer.py
    python3 grpc_infer.py fd001-065
    python3 grpc_infer.py --all
"""

import array
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
    engines, walk = common.selected_engines(sys.argv)
    metadata = (("authorization", common.authorization_value()),)
    channel = grpc.insecure_channel(common.grpc_target(), options=CHANNEL_OPTIONS)
    stub = pb_grpc.GRPCInferenceServiceStub(channel)
    try:
        if not walk:
            _report(stub, metadata, engines[0])
            return
        matched = 0
        for engine in engines:
            try:
                ok, line = _line(stub, metadata, engine)
            except grpc.RpcError as err:
                print(f"{engine['id']}  error: {err.code().name}: {err.details()}")
                continue
            except (Exception, SystemExit) as err:
                print(f"{engine['id']}  error: {common.walk_error(err)}")
                continue
            print(line)
            matched += int(ok)
        common.finish_walk(matched, len(engines))
    finally:
        channel.close()


def _report(stub, metadata, engine):
    try:
        response, profile = _infer(stub, metadata, engine)
    except grpc.RpcError as err:
        print(f"{err.code().name}: {err.details()}", file=sys.stderr)
        raise SystemExit(1) from err
    common.report_rul(
        engine,
        response.model_name,
        response.model_version,
        profile,
        _rul(response),
    )


def _line(stub, metadata, engine):
    response, profile = _infer(stub, metadata, engine)
    return common.rul_line(engine, profile, _rul(response))


def _infer(stub, metadata, engine):
    profile, tensor = common.prepare(engine)
    request = pb.ModelInferRequest(
        model_name=common.model_name(),
        id=engine["id"],
        inputs=[
            pb.ModelInferRequest.InferInputTensor(
                name=common.INPUT_NAME,
                datatype="FP32",
                shape=[1, common.WINDOW, common.CHANNELS],
            )
        ],
        raw_input_contents=[tensor],
    )
    request.parameters["binary_data_output"].bool_param = True
    return stub.ModelInfer(request, metadata=metadata, timeout=120), profile


def _rul(response):
    chosen = None
    index = 0
    for i, tensor in enumerate(response.outputs):
        if tensor.name == common.OUTPUT_NAME:
            chosen = tensor
            index = i
            break
    if chosen is None:
        names = [tensor.name for tensor in response.outputs]
        raise SystemExit(f"response has no {common.OUTPUT_NAME}: {names}")
    shape = [int(dim) for dim in chosen.shape]
    if index < len(response.raw_output_contents) and response.raw_output_contents[index]:
        blob = response.raw_output_contents[index]
        values = array.array("f")
        values.frombytes(blob)
        if sys.byteorder != "little":
            values.byteswap()
        return common.rul_value(shape, values)
    return common.rul_value(shape, array.array("f", chosen.contents.fp32_contents))


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
