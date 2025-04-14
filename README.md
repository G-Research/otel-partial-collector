# Partial collector

Repository contains two components that are intended to scale separately.

1. Otel Partial Exporter
2. Otel Partial Receiver

Both of these components connect to the same Postgresql database. The exporter is responsible for writing/removing partial traces, while the receiver
is responsible for sending partial traces through the pipeline when partial span should be collected.

## Otel Partial Exporter

Otel Partial Exporter receives logs. Inside the log, the body field is base64 protobuf encoded trace.

When the log is received, `partial.event` is extracted from the log attributes. If it doesn't exist, the log will be ignored.

Valid values for the `partial.event` attribute are:
- `heartbeat`: This event stores the OTLP Trace serialized as protobuf into the database.
- `stop`: This event removes the partial events associated with that trace from the database since the trace is already propagated using the trace pipeline.

Each log should contain the `partial.frequency` attribute as well. This attribute is used to express the desired frequency for heartbeats sent for the trace.
The frequency is multiplied with the `expiry_factor` to set the expiration time of the span. After each heartbeat, the `timestamp` and the `expires_at` fields
are updated by setting `timestamp` to `NOW`, and the `expires_at` to `NOW + (duration(frequency) * expiry_factor)`.

This configuration parameter is configured on the exporter so proper indexing could be done. Then the whole job of the receiver is to collect the traces that
are expired, leveraging power of indexing.

Each trace inside the database contains a single span. Partial exporter takes attributes from the log (excluding ones with `partial.` prefix), and merges them
with the span attributes. If attribute is already present in a span, the span attribute takes precedence.

## Otel Partial Receiver

Otel Partial Receiver is responsible for monitoring old traces inside the database. It uses the `gc_interval` to query old traces at specified interval + the jitter.
The jitter is a random value applied to the interval, so if there are multiple deployments of the receiver collecting expired traces running on the same clock,
they shouldn't constantly overlap.

The receiver removes the expired traces them from the database if they are propagated successfully through the pipeline.
If the send fails, the trace will stay in the database and will be subject for the next cycle.

Each partial trace pushed by the Otel Partial Receiver contains the `partial.gc` attribute set to `true` to distinguish spans pushed by the receiver.

### Developer setup

The assumption is that you are in the repository root.

#### Steps
1. Run postgres
```bash
docker run --name otel-partial-collector-db -e POSTGRES_DB="otelpartialcollector" -e POSTGRES_PASSWORD=test -d -p 40444:5432 --rm postgres:latest
```
2. Apply migration:
```bash
migrate -source file:///$(pwd)/internal/postgres/migrations -database "postgres://postgres:test@localhost:40444/otelpartialcollector?sslmode=disable" up
```
The tool used can be installed from [here](https://github.com/golang-migrate/migrate/tree/master/cmd/migrate)

3. Generate binary:

```bash
go run go.opentelemetry.io/collector/cmd/builder@v0.122.1 --config ./example/builder-config.yaml
```

4. Run the app

```bash
./bin/otel-partial-collector --config example/config.yaml
```

