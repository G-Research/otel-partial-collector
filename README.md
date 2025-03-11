# Partial collector

Repository contains two components that are intended to scale separately.

1. Otel Partial Exporter
2. Otel Partial Receiver

Both of these components connect to the same Postgresql database. The exporter is responsible for writing/removing partial traces, while the receiver
is responsible for sending partial traces through the pipeline when partial span is not received for the `gc_threshold` duration.

## Otel Partial Exporter

Otel Partial Exporter receives logs. Inside the log, the body field is base64 protobuf encoded trace.

When the log is received, `partial.event` is extracted from the log attributes. If it doesn't exist, the log will be ignored.

Valid values for the `partial.event` attribute are:
- `heartbeat`: This event stores the OTLP Trace serialized as protobuf into the database.
- `stop`: This event removes the partial events associated with that trace from the database since the trace is already propagated using the trace pipeline.

Each trace inside the database contains a single span. Partial exporter takes attributes from the log (excluding ones with `partial.` prefix), and merges them
with the span attributes. If attribute is already present in a span, the span attribute takes precedence.

## Otel Partial Receiver

Otel Partial Receiver is responsible for monitoring old traces inside the database. It uses the `gc_threshold` to query old traces, remove them from the database
and send them through the pipeline. If the send fails, the record will stay in the database and will be subject for the next push.

Each partial trace pushed by the Otel Partial Receiver contains the `partial.gc` attribute set to `true` to distinguish spans pushed by the receiver.


### Developer setup

The assumption is that you are in the repository root.

#### Steps
1. Run postgers
```bash
docker run --name otel-partial-collector-db -e POSTGRES_DB="otelpartialcollector" -e POSTGRES_PASSWORD=test -d -p 40444:5432 --rm postgres:latest
```
2. Apply migration:
```bash
migrate -source file:///$(pwd)/migrations -database "postgres://postgres:test@localhost:40444/otelpartialcollector?sslmode=disable" up
```
The tool used can be installed from [here](https://github.com/golang-migrate/migrate/tree/master/cmd/migrate)

3. Generate binary:

```bash
mkdir bin || true
go run go.opentelemetry.io/collector/cmd/builder@v0.121.0 --config ./example/builder-config.yaml
```

4. Run the app

```bash
./bin/otel-partial-span --config example/config.yaml
```

## Important links

- [Installation of the ocb](https://opentelemetry.io/docs/collector/custom-collector/#step-1---install-the-builder)
- [Builder manifest documentation](https://opentelemetry.io/docs/collector/custom-collector/#step-2---create-a-builder-manifest-file)
