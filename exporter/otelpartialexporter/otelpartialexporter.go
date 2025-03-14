package otelpartialexporter

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/G-Research/otel-partial-collector/internal/postgres"
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
	// Postgres is URL used to connect to the postgres instance
	Postgres string `mapstructure:"postgres"`
	// ExpiryFactor multiplies the heartbeat interval with the ExpiryFactor
	// to get the expiration time for the trace.
	ExpiryFactor int `mapstructure:"expiry_factor"`
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

	db           *postgres.DB
	expiryFactor int

	logger *zap.Logger

	cancelFunc context.CancelFunc
	component.StartFunc
}

func (e *otelPartialExporter) Shutdown(ctx context.Context) error {
	e.logger.Info("Shutting down otel partial exporter")
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
	now := time.Now().UTC()
	var errs []error
	resourceLogs := logs.ResourceLogs()
	for i := range resourceLogs.Len() {
		resourceLog := resourceLogs.At(i)
		resourceAttrs := resourceLog.Resource().Attributes()
		scopeLogs := resourceLog.ScopeLogs()
		for j := range scopeLogs.Len() {
			records := scopeLogs.At(j).LogRecords()

			for k := range records.Len() {
				logRecord := records.At(k)
				logAttrs := logRecord.Attributes()

				eventType, err := getEventTypeFromAttributes(logAttrs)
				if err != nil {
					e.logger.Warn("Failed to resolve event type", zap.Error(err))
					continue
				}

				interval, err := getHeartbeetIntervalFromAttributes(logAttrs)
				if err != nil {
					e.logger.Warn("Failed to resolve heartbeat frequency", zap.Error(err))
					continue
				}

				rawTrace, err := base64.StdEncoding.DecodeString(logRecord.Body().AsString())
				if err != nil {
					e.logger.Error("failed to base64 decode trace", zap.Error(err))
					continue
				}

				traces, err := tracesProtoUnmarshaler.UnmarshalTraces(rawTrace)
				if err != nil {
					return fmt.Errorf("failed to unmarshal traces: %v", err)
				}

				switch eventType {
				case EventTypeHeartbeet:
					for _, t := range flattenTraces(traces) {
						mergeAttributes(t.ResourceSpans().At(0).Resource().Attributes(), resourceAttrs)
						span := t.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)

						b, err := tracesProtoMarshaler.MarshalTraces(t)
						if err != nil {
							errs = append(errs, fmt.Errorf("failed to marshal trace %v: %w", t, err))
							continue
						}

						if err := e.db.PutTrace(
							ctx,
							&postgres.PartialTrace{
								TraceID:   span.TraceID().String(),
								SpanID:    span.SpanID().String(),
								Trace:     b,
								Timestamp: now,
								ExpiresAt: now.Add(interval * time.Duration(e.expiryFactor)),
							},
						); err != nil {
							errs = append(errs, fmt.Errorf("failed to put trace: %w", err))
							continue
						}
					}
				case EventTypeStop:
					for _, t := range flattenTraces(traces) {
						span := t.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
						if err := e.db.RemoveTrace(ctx, span.TraceID().String(), span.SpanID().String()); err != nil {
							errs = append(errs, fmt.Errorf("failed to remove trace: %w", err))
							continue
						}
					}

				default:
					// assertion
					panic("unreachable")
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
		db:           db,
		expiryFactor: cfg.ExpiryFactor,
		logger:       settings.Logger,
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

type EventType int

const (
	EventTypeUnknown = iota
	EventTypeHeartbeet
	EventTypeStop
)

func getEventTypeFromAttributes(attrs pcommon.Map) (EventType, error) {
	v, ok := attrs.Get("partial.event")
	if !ok {
		return EventTypeUnknown, fmt.Errorf("unknown event type: empty")
	}
	switch t := v.AsString(); t {
	case "heartbeat":
		return EventTypeHeartbeet, nil
	case "stop":
		return EventTypeStop, nil
	default:
		return EventTypeUnknown, fmt.Errorf("unknown event type: %q", t)
	}
}

func getHeartbeetIntervalFromAttributes(attrs pcommon.Map) (time.Duration, error) {
	freq, ok := attrs.Get("partial.frequency")
	if !ok {
		return 0, fmt.Errorf("frequency is not set")
	}
	d, err := time.ParseDuration(freq.AsString())
	if err != nil {
		return 0, fmt.Errorf("failed to parse duration: %w", err)
	}
	return d, nil
}

func mergeAttributes(dst pcommon.Map, sources ...pcommon.Map) {
	for _, src := range sources {
		src.Range(func(k string, v pcommon.Value) bool {
			if strings.HasPrefix(k, "partial.") {
				return true
			}

			_, ok := dst.Get(k)
			if ok {
				return true
			}

			switch v.Type() {
			case pcommon.ValueTypeBool:
				dst.PutBool(k, v.Bool())
			case pcommon.ValueTypeBytes:
				bytes := dst.PutEmptyBytes(k)
				v.Bytes().MoveTo(bytes)
			case pcommon.ValueTypeDouble:
				dst.PutDouble(k, v.Double())
			case pcommon.ValueTypeInt:
				dst.PutInt(k, v.Int())
			case pcommon.ValueTypeMap:
				m := dst.PutEmptyMap(k)
				v.Map().MoveTo(m)
			case pcommon.ValueTypeStr:
				dst.PutStr(k, v.Str())
			case pcommon.ValueTypeEmpty:
				dst.PutEmpty(k)
			case pcommon.ValueTypeSlice:
				s := dst.PutEmptySlice(k)
				v.Slice().MoveAndAppendTo(s)
			}
			return true
		})
	}
}
