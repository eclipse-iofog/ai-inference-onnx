"""gRPC infer for one wafer image, or every image under assets/wafers.

Requires SERVICEACCOUNT_PATH and INFERENCE_GRPC_TARGET (host:port).
The channel is plaintext h2c. The default image is assets/wafers/wafer-scratch-001.jpg.

    python3 grpc_infer.py
    python3 grpc_infer.py /opt/scenarios/assets/wafers/wafer-donut-001.jpg
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
    images, walk = common.selected_images(sys.argv)
    metadata = (("authorization", common.authorization_value()),)
    channel = grpc.insecure_channel(common.grpc_target(), options=CHANNEL_OPTIONS)
    stub = pb_grpc.GRPCInferenceServiceStub(channel)
    try:
        if not walk:
            _report(stub, metadata, images[0])
            return
        detected = 0
        for image in images:
            try:
                ok, line = _line(stub, metadata, image)
            except grpc.RpcError as err:
                print(f"{image.name}  error: {err.code().name}: {err.details()}")
                continue
            except (Exception, SystemExit) as err:
                print(f"{image.name}  error: {common.walk_error(err)}")
                continue
            print(line)
            detected += int(ok)
        common.finish_walk(detected, len(images))
    finally:
        channel.close()


def _report(stub, metadata, image):
    try:
        response = _infer(stub, metadata, image)
    except grpc.RpcError as err:
        print(f"{err.code().name}: {err.details()}", file=sys.stderr)
        raise SystemExit(1) from err
    shape, values = _output(response)
    common.report_detection(image.name, response.model_name, response.model_version, shape, values)


def _line(stub, metadata, image):
    response = _infer(stub, metadata, image)
    shape, values = _output(response)
    return common.detection_line(image.name, shape, values)


def _infer(stub, metadata, image):
    tensor = common.image_tensor(image)
    request = pb.ModelInferRequest(
        model_name=common.model_name(),
        id=image.stem,
        inputs=[
            pb.ModelInferRequest.InferInputTensor(
                name=common.INPUT_NAME,
                datatype="FP32",
                shape=[1, 3, common.SIDE, common.SIDE],
            )
        ],
        raw_input_contents=[tensor],
    )
    request.parameters["binary_data_output"].bool_param = True
    return stub.ModelInfer(request, metadata=metadata, timeout=120)


def _output(response):
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
        return shape, values
    return shape, array.array("f", chosen.contents.fp32_contents)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
