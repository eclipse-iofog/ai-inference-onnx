# Tasks

These pages are the jobs that get a model ready to serve.

- [Run the container](docker-run.md) starts the image with Docker or Podman.
- [Bind a model from Edgelet](edgelet-bind.md) uses the catalog example.
- [Choose the ONNX file](choose-onnx-file.md) decides which file in the directory serves.
- [Write config.pbtxt](config-pbtxt.md) sets the fields this engine reads.
- [Add a knowledge item](knowledge.md) names a directory the model waits for.
- [Call infer on REST](infer-rest.md) sends the add model on port 8000.
- [Call infer on gRPC](infer-grpc.md) sends the add model on port 8001.
- [Call infer on NATS](infer-nats.md) publishes the add model on its infer subject.
- [Send a binary tensor](binary-tensor.md) puts the tensor bytes after the JSON.
- [Read a reload](reload.md) follows a model through `LOADING`.
- [Read the dashboard](dashboard.md) opens Nodes, Models, and Engine on port 8080.
