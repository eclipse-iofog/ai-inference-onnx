# Reference

These pages describe the repository the engine loads, the server settings, authentication, the three transports, the metric names, the image tags, and one catalog example.

- [Repository](repository.md) describes which file in a model directory is served, and the `knowledge` name file.
- [Configuration](configuration.md) lists every server key, the listen ports, and the log.
- [Authentication](auth.md) describes `disabled`, `edgelet`, and `static`.
- [REST](rest.md) lists the HTTP calls, including the add-model infer.
- [gRPC](grpc.md) lists the same calls on h2c.
- [NATS](nats.md) lists the subjects, inline transfer, and rendezvous.
- [Metrics](metrics.md) lists the scrape names and the snapshot subject.
- [Images](images.md) lists the tags, the variants, and how a device is passed in.
- [Catalog](catalog.md) is one application that binds a model, a knowledge item, the NATS user rules, and the ports.
