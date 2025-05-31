package otelpartialexporter

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/G-Research/otel-partial-collector/internal/postgres"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

var typeStr = component.MustNewType("otelpartialexporter")

var (
	tracesProtoUnmarshaler base64ProtoUnmarshaler
	tracesProtoMarshaler   ptrace.ProtoMarshaler
	tracesJSONUnmarshaler  ptrace.JSONUnmarshaler
)

type base64ProtoUnmarshaler struct{}

func (*base64ProtoUnmarshaler) UnmarshalTraces(buf []byte) (ptrace.Traces, error) {
	rawTrace, err := base64.StdEncoding.DecodeString(string(buf))
	if err != nil {
		return ptrace.Traces{}, fmt.Errorf("failed to base64 decode: %w", err)
	}

	var u ptrace.ProtoUnmarshaler
	traces, err := u.UnmarshalTraces(rawTrace)
	if err != nil {
		return ptrace.Traces{}, fmt.Errorf("failed to unmarshal traces: %w", err)
	}

	return traces, nil
}

type otelPartialExporter struct {
	db           *postgres.DB
	expiryFactor int

	logger *zap.Logger

	cancelFunc context.CancelFunc
	component.StartFunc
}

func (e *otelPartialExporter) Shutdown(context.Context) error {
	e.logger.Info("Shutting down otel partial exporter")
	if e.cancelFunc != nil {
		e.cancelFunc()
	}
	return e.db.Close()
}

func (e *otelPartialExporter) consumeLogs(ctx context.Context, logs plog.Logs) error {
	// These structures let us deduplicate events for the same span in a given batch.
	heartbeatTraces := make(map[postgres.PartialTraceKey]*postgres.PartialTrace)
	stopTraces := make(map[postgres.PartialTraceKey]any)

	// Go through all the received logs to prepare them for the database transaction.
	// Any issue with a given log at this stage will be permanent and the log should thus be skipped.
	now := time.Now().UTC()
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

				unmarshaler, ok := getUnmarshaler(logAttrs)
				if !ok {
					e.logger.Warn("Failed to resolve unmarshaler type")
					continue
				}

				traces, err := unmarshaler.UnmarshalTraces([]byte(logRecord.Body().AsString()))
				if err != nil {
					e.logger.Warn("Failed to unmarshal traces", zap.Error(err))
					continue
				}

				switch eventType {
				case EventTypeHeartbeat:
					// if heartbeat, get the frequency
					interval, err := getHeartbeatIntervalFromAttributes(logAttrs)
					if err != nil {
						e.logger.Warn("Failed to resolve heartbeat frequency", zap.Error(err))
						continue
					}

					for _, t := range flattenTraces(traces) {
						mergeAttributes(t.ResourceSpans().At(0).Resource().Attributes(), resourceAttrs)
						span := t.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)

						b, err := tracesProtoMarshaler.MarshalTraces(t)
						if err != nil {
							e.logger.Warn("Failed to marshal trace", zap.Any("trace", t), zap.Error(err))
							continue
						}

						key := postgres.PartialTraceKey{
							TraceID: span.TraceID().String(),
							SpanID:  span.SpanID().String(),
						}
						heartbeatTraces[key] = &postgres.PartialTrace{
							PartialTraceKey: key,
							Trace:           b,
							Timestamp:       now,
							ExpiresAt:       now.Add(interval * time.Duration(e.expiryFactor)),
						}
					}

				case EventTypeStop:
					for _, t := range flattenTraces(traces) {
						span := t.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
						stopTraces[postgres.PartialTraceKey{
							TraceID: span.TraceID().String(),
							SpanID:  span.SpanID().String(),
						}] = nil
					}

				default:
					// assertion
					panic("unreachable")
				}
			}
		}
	}

	// If a given span has sent both a heartbeat and a stop event in the same
	// batch, we do not need to insert it at all.
	for k := range stopTraces {
		delete(heartbeatTraces, k)
	}

	// If the DB transaction fails, we return an error and the pipeline will retry.
	if err := e.db.Transact(
		ctx,
		pgx.TxOptions{
			IsoLevel:       pgx.Serializable,
			AccessMode:     pgx.ReadWrite,
			DeferrableMode: pgx.NotDeferrable,
		},
		func(ctx context.Context, db *postgres.DB) error {
			if err := db.PutTraces(ctx, slices.Collect(maps.Values(heartbeatTraces))); err != nil {
				return fmt.Errorf("failed to put traces: %w", err)
			}

			if err := db.RemoveTraces(ctx, slices.Collect(maps.Keys(stopTraces))); err != nil {
				return fmt.Errorf("failed to delete traces: %w", err)
			}

			return nil
		},
	); err != nil {
		return fmt.Errorf("transaction error: %w", err)
	}

	return nil
}

func newPartialExporter(ctx context.Context, settings exporter.Settings, baseCfg component.Config) (exporter.Logs, error) {
	cfg := baseCfg.(*Config)
	db, err := postgres.NewDB(ctx, cfg.Postgres)
	if err != nil {
		return nil, fmt.Errorf("failed to create new db connection: %w", err)
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
		createDefaultConfig,
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
	EventTypeHeartbeat
	EventTypeStop
)

func getEventTypeFromAttributes(attrs pcommon.Map) (EventType, error) {
	v, ok := attrs.Get("partial.event")
	if !ok {
		return EventTypeUnknown, errors.New("unknown event type: empty")
	}
	switch t := v.AsString(); t {
	case "heartbeat":
		return EventTypeHeartbeat, nil
	case "stop":
		return EventTypeStop, nil
	default:
		return EventTypeUnknown, fmt.Errorf("unknown event type: %q", t)
	}
}

func getUnmarshaler(attrs pcommon.Map) (ptrace.Unmarshaler, bool) {
	ty, ok := attrs.Get("partial.body.type")
	if !ok {
		return &tracesProtoUnmarshaler, true
	}

	switch ty.AsString() {
	case "proto":
		return &tracesProtoUnmarshaler, true
	case "json/v1":
		return &tracesJSONUnmarshaler, true
	default:
		return nil, false
	}
}

func getHeartbeatIntervalFromAttributes(attrs pcommon.Map) (time.Duration, error) {
	freq, ok := attrs.Get("partial.frequency")
	if !ok {
		return 0, errors.New("frequency is not set")
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
