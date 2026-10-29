# Reference

These pages describe the repository the engine loads, the server settings, how a request is scheduled, the tensor wire format, authentication, the three transports, the metric names, the image tags, and one catalog example.

Read [the user guide](../user-guide/) in order when you are learning the engine. Use this directory when you need one fact.

- [Repository](repository.md) describes which file in a model directory is served, the `knowledge` name file, and when a reload starts.
- [Configuration](configuration.md) lists every server key, the listen ports, and the log.
- [Scheduling](scheduling.md) lists the queue, the batch cap, the delay, the thread counts, and the payload limit.
- [Tensors](tensors.md) lists the datatypes, the JSON forms, and the binary layout.
- [Authentication](auth.md) describes `disabled`, `edgelet`, and `static`.
- [REST](rest.md) lists the HTTP calls, including the add-model infer.
- [gRPC](grpc.md) lists the same calls on h2c.
- [NATS](nats.md) lists the subjects, inline transfer, and rendezvous.
- [Metrics](metrics.md) lists the scrape names and the snapshot subject.
- [Images](images.md) lists the tags, the variants, the cache volume, and how a device group is passed in.
- [Catalog](catalog.md) is one application that binds a model, a knowledge item, the NATS user rules, the cache volume, and the ports.
