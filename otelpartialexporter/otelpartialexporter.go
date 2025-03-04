package otelpartialexporter

import (
	"context"
	"fmt"

	"github.com/G-Research/otel-partial-connector/postgres"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
)

var typeStr = component.MustNewType("otelpartialexporter")

var (
	logsJSONMarshaler      plog.JSONMarshaler
	tracesProtoUnmarshaler ptrace.ProtoUnmarshaler
	tracesProtoMarshaler   ptrace.ProtoMarshaler
)

type Config struct {
	Postgres string `mapstructure:"postgres"`
}

func Default() component.Config {
	return &Config{}
}

func (c *Config) Validate() error {
	if _, err := pgx.ParseConfig(c.Postgres); err != nil {
		return fmt.Errorf("invalid postgres config: %v", err)
	}

	return nil
}

type otelPartialExporter struct {
	exporter exporter.Logs
	host     component.Host

	db *postgres.DB

	logger *zap.Logger

	cancelFunc context.CancelFunc
	component.StartFunc
}

func (e *otelPartialExporter) Shutdown(ctx context.Context) error {
	if e.cancelFunc != nil {
		e.cancelFunc()
	}
	return e.db.Close(ctx)
}

func (e *otelPartialExporter) consumeLogs(ctx context.Context, logs plog.Logs) error {
	out, err := logsJSONMarshaler.MarshalLogs(logs)
	if err != nil {
		return err
	}
	e.logger.Debug("Consuming logs", zap.String("log", string(out)))

	resourceLogs := logs.ResourceLogs()
	for i := range resourceLogs.Len() {
		resourceLog := resourceLogs.At(i)
		scopeLogs := resourceLog.ScopeLogs()
		for j := range scopeLogs.Len() {
			records := scopeLogs.At(j).LogRecords()

			for k := range records.Len() {
				logRecord := records.At(k)
				attrs := logRecord.Attributes()
				value, ok := attrs.Get("partial.event")
				if !ok {
					continue
				}
				val := value.Str()

				traces, err := tracesProtoUnmarshaler.UnmarshalTraces(logRecord.Body().Bytes().AsRaw())
				if err != nil {
					return fmt.Errorf("failed to unmarshal traces: %v", err)
				}

				switch val {
				case "heartbeat":
					if err := e.putTraces(ctx, traces); err != nil {
						return fmt.Errorf("failed to put races: %v", err)
					}
				case "stop":
					if err := e.removeTraces(ctx, traces); err != nil {
						return fmt.Errorf("failed to delete traces: %v", err)
					}
				default:
					e.logger.Error("Unknown attribute value", zap.String("partial.event", val))
				}
			}
		}
	}

	return nil
}

func newPartialExporter(ctx context.Context, settings exporter.Settings, baseCfg component.Config) (exporter.Logs, error) {
	cfg := baseCfg.(*Config)
	db, err := postgres.NewDB(ctx, cfg.Postgres)
	if err != nil {
		return nil, fmt.Errorf("failed to create new db connection: %v", err)
	}

	ex := &otelPartialExporter{
		db:     db,
		logger: nil,
	}

	return exporterhelper.NewLogs(
		ctx,
		settings,
		baseCfg,
		ex.consumeLogs,
		exporterhelper.WithCapabilities(consumer.Capabilities{MutatesData: true}),
	)
}

func NewFactory() exporter.Factory {
	return exporter.NewFactory(
		typeStr,
		func() component.Config { return &Config{} },
		exporter.WithLogs(newPartialExporter, component.StabilityLevelAlpha),
	)
}
