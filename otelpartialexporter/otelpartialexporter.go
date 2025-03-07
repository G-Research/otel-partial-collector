package otelpartialexporter

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/G-Research/otel-partial-connector/postgres"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/pcommon"
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

func defaultConfig() component.Config {
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
	// TODO: maybe check the level and use debug log here
	out, err := logsJSONMarshaler.MarshalLogs(logs)
	if err != nil {
		return err
	}

	e.logger.Info("Consuming logs", zap.String("log", string(out)))

	var errs []error
	resourceLogs := logs.ResourceLogs()
	for i := range resourceLogs.Len() {
		resourceLog := resourceLogs.At(i)
		scopeLogs := resourceLog.ScopeLogs()
		for j := range scopeLogs.Len() {
			records := scopeLogs.At(j).LogRecords()

			for k := range records.Len() {
				logRecord := records.At(k)
				logAttrs := logRecord.Attributes()
				value, ok := logAttrs.Get("partial.event")
				if !ok {
					continue
				}
				val := value.Str()

				rawTrace, err := base64.StdEncoding.DecodeString(logRecord.Body().AsString())
				if err != nil {
					e.logger.Error("failed to base64 decode trace", zap.Error(err))
					continue
				}

				traces, err := tracesProtoUnmarshaler.UnmarshalTraces(rawTrace)
				if err != nil {
					return fmt.Errorf("failed to unmarshal traces: %v", err)
				}

				switch val {
				case "heartbeat":
					for _, t := range flattenTraces(traces) {
						b, err := tracesProtoMarshaler.MarshalTraces(t)
						if err != nil {
							errs = append(errs, fmt.Errorf("failed to marshal trace %v: %w", t, err))
							continue
						}

						span := t.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)

						spanAttrs := span.Attributes()
						logAttrs.Range(func(k string, v pcommon.Value) bool {
							if !strings.HasPrefix(k, "partial.") {
								return true
							}

							_, ok := spanAttrs.Get(k)
							if ok {
								return true
							}

							switch v.Type() {
							case pcommon.ValueTypeBool:
								spanAttrs.PutBool(k, v.Bool())
							case pcommon.ValueTypeBytes:
								bytes := spanAttrs.PutEmptyBytes(k)
								v.Bytes().MoveTo(bytes)
							case pcommon.ValueTypeDouble:
								spanAttrs.PutDouble(k, v.Double())
							case pcommon.ValueTypeInt:
								spanAttrs.PutInt(k, v.Int())
							case pcommon.ValueTypeMap:
								m := spanAttrs.PutEmptyMap(k)
								v.Map().MoveTo(m)
							case pcommon.ValueTypeSlice:
								s := spanAttrs.PutEmptySlice(k)
								v.Slice().MoveAndAppendTo(s)
							}
							return true
						})

						if err := e.db.PutTrace(
							ctx,
							&postgres.PartialTrace{
								TraceID: span.TraceID().String(),
								SpanID:  span.SpanID().String(),
								Trace:   b,
							},
						); err != nil {
							errs = append(errs, fmt.Errorf("failed to put trace: %w", err))
							continue
						}
					}
				case "stop":
					for _, t := range flattenTraces(traces) {
						span := t.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
						if err := e.db.RemoveTrace(ctx, span.TraceID().String(), span.SpanID().String()); err != nil {
							errs = append(errs, fmt.Errorf("failed to remove trace: %w", err))
							continue
						}
					}

				default:
					e.logger.Error("Unknown attribute value", zap.String("partial.event", val))
				}
			}
		}
	}

	return errors.Join(errs...)
}

func newPartialExporter(ctx context.Context, settings exporter.Settings, baseCfg component.Config) (exporter.Logs, error) {
	cfg := baseCfg.(*Config)
	db, err := postgres.NewDB(ctx, cfg.Postgres)
	if err != nil {
		return nil, fmt.Errorf("failed to create new db connection: %v", err)
	}

	ex := &otelPartialExporter{
		db:     db,
		logger: settings.Logger,
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
		defaultConfig,
		exporter.WithLogs(
			newPartialExporter,
			component.StabilityLevelAlpha,
		),
	)
}

func flattenTraces(traces ptrace.Traces) []ptrace.Traces {
	spanCount := traces.SpanCount()
	if spanCount == 1 {
		return []ptrace.Traces{traces}
	}

	newTraces := make([]ptrace.Traces, 0, spanCount)
	resourceSpans := traces.ResourceSpans()
	for i := range resourceSpans.Len() {
		resourceSpan := resourceSpans.At(i)
		resource := resourceSpan.Resource()
		scopeSpans := resourceSpan.ScopeSpans()
		for j := range scopeSpans.Len() {
			scopeSpan := scopeSpans.At(j)
			scope := scopeSpan.Scope()
			spans := scopeSpan.Spans()
			for k := range spans.Len() {
				span := spans.At(k)

				newTrace := ptrace.NewTraces()
				newResourceSpans := newTrace.ResourceSpans()
				newResourceSpan := newResourceSpans.AppendEmpty()
				newResourceSpan.SetSchemaUrl(resourceSpan.SchemaUrl())
				newResource := newResourceSpan.Resource()
				resource.CopyTo(newResource)
				newScopeSpans := newResourceSpan.ScopeSpans()
				newScopeSpan := newScopeSpans.AppendEmpty()
				newScopeSpan.SetSchemaUrl(scopeSpan.SchemaUrl())
				newScope := newScopeSpan.Scope()
				scope.CopyTo(newScope)
				newSpans := newScopeSpan.Spans()
				newSpan := newSpans.AppendEmpty()
				span.CopyTo(newSpan)

				newTraces = append(newTraces, newTrace)
			}
		}
	}

	return newTraces
}
