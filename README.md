# Partial connector

Partial connector is a custom implementation of [OTEP Connector](https://opentelemetry.io/docs/collector/building/connector/) where connector
transforms logs to traces.

When the log is received, `partial.event` is extracted from the log attributes. If it doesn't exist, the log will be ignored.

Valid values for the `partial.event` attribute are:
- `heartbeat`: This event stores the OTLP Trace serialized as protobuf into the database.
- `stop`: This event removes the partial events associated with that trace from the database since the trace is already propagated using the trace pipeline.

The connector runs a background process, checking partial traces stored inside the database. If the trace is older than `config.gc_older_than`, the connector will push the trace with the `connector.gc` attribute set to `true`. This way, users can distinguish what traces are pushed by the connector.

## Run locally

To run the connector, you first should generate a binary file. In order to do it
you should use ocb. Install ocb by referring to [this](https://opentelemetry.io/docs/collector/custom-collector/#step-1---install-the-builder) page.

Once you have the ocb, you can use `example/builder-config.yaml` to test this application.
However, you should probably generate a new one more suitable for your environment.
The builder manifest is documented [here](https://opentelemetry.io/docs/collector/custom-collector/#step-2---create-a-builder-manifest-file)

### Developer setup

The assumption is that you are in the repository root.

#### Steps
1. Run postgers
```bash
docker run --name otel-partial-connector-db -e POSTGRES_DB="otelpartialconnector" -e POSTGRES_PASSWORD=test -d -p 40444:5432 --rm postgres:latest
```
2. Apply migration:
```bash
migrate -source file:///$(pwd)/migrations -database "postgres://postgres:test@localhost:40444/otelpartialconnector?sslmode=disable" up
```
The tool used can be installed from [here](https://github.com/golang-migrate/migrate/tree/master/cmd/migrate)

3. Generate binary:

```bash
mkdir cmd || true
./bin/ocb --config example/builder-config.yaml
```

4. Run the app

```bash
./cmd/otelpartialconnector --config example/config.yaml
```
