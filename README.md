# Run locally

To run the connector, you first should generate a binary file. In order to do it
you should use ocb. Install ocb by referring to [this](https://opentelemetry.io/docs/collector/custom-collector/#step-1---install-the-builder) page.

Once you have the ocb, you can use `example/builder-config.yaml` to test this application.
However, you should probably generate a new one more suitable for your environment.
The builder manifest is documented [here](https://opentelemetry.io/docs/collector/custom-collector/#step-2---create-a-builder-manifest-file)

## Developer setup

The assumption is that you are in the repository root.

## Steps
1. Run postgers
```bash
docker run --name ltt -e POSTGRES_DB="partialconnector" -e POSTGRES_PASSWORD=test -d -p 40444:5432 --rm postgres:latest
```
2. Apply migration:
```bash
migrate -source file:///$(pwd)/migrations -database "postgres://postgres:test@localhost:40444/partialconnector?sslmode=disable" up
```
The tool used can be installed from [here](https://github.com/golang-migrate/migrate/tree/master/cmd/migrate)

3. Generate binary:

```bash
mkdir cmd || true
./bin/ocb --config example/builder-config.yaml
```

4. Run the app

```bash
./cmd/partialconnector --config example/config.yaml
```
