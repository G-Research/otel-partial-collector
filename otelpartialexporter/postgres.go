package otelpartialexporter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"
)

func (e *otelPartialExporter) putTraces(ctx context.Context, traces ptrace.Traces) error {
	if traces.SpanCount() == 1 {
		return e.putTrace(ctx, traces)
	}

	var errs []error
	for _, trace := range flattenTraces(traces) {
		if err := e.putTrace(ctx, trace); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (e *otelPartialExporter) putTrace(ctx context.Context, trace ptrace.Traces) error {
	q := `
INSERT INTO partial_traces
(key, value, timestamp)
VALUES
($1, $2, NOW())
ON CONFLICT (key) DO UPDATE
SET value = $2, timestamp = NOW()
`

	span := trace.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	traceKey := traceKey{
		TraceID: span.TraceID().String(),
		SpanID:  span.SpanID().String(),
	}

	val, err := tracesProtoMarshaler.MarshalTraces(trace)
	if err != nil {
		return fmt.Errorf("failed to marshal trace: %v", err)
	}

	if _, err := e.db.Exec(ctx, q, traceKey.Key(), val); err != nil {
		return fmt.Errorf("failed to insert partial span: %w", err)
	}

	return nil
}

func (e *otelPartialExporter) removeTraces(ctx context.Context, traces ptrace.Traces) error {
	if traces.SpanCount() == 1 {
		return e.removeTrace(ctx, traces)
	}

	var errs []error
	for _, trace := range flattenTraces(traces) {
		if err := e.removeTrace(ctx, trace); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (e *otelPartialExporter) removeTrace(ctx context.Context, trace ptrace.Traces) error {
	q := `
DELETE FROM partial_traces
WHERE key = $1
	`

	span := trace.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	traceKey := traceKey{
		TraceID: span.TraceID().String(),
		SpanID:  span.SpanID().String(),
	}

	if _, err := e.db.Exec(ctx, q, traceKey.Key()); err != nil {
		return fmt.Errorf("failed to delete partial span: %w", err)
	}

	return nil
}

func (e *otelPartialExporter) GetTracesOlderThan(ctx context.Context, timestamp time.Time) ([]ptrace.Traces, error) {
	q := `
SELECT value FROM partial_traces
WHERE timestamp < $1
	`

	rows, err := e.db.Query(ctx, q, timestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to query traces")
	}
	defer rows.Close()

	var traces []ptrace.Traces
	var bytes []byte
	for rows.Next() {
		if err := rows.Scan(&bytes); err != nil {
			return nil, fmt.Errorf("failed to scan row: %v", err)
		}

		trace, err := tracesProtoUnmarshaler.UnmarshalTraces(bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal trace: %v", err)
		}

		traces = append(traces, trace)
		bytes = bytes[:0]
	}

	return traces, nil
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

type traceKey struct {
	TraceID string
	SpanID  string
}

func (id *traceKey) Key() string {
	return fmt.Sprintf("trace:%s:span:%s", id.TraceID, id.SpanID)
}
