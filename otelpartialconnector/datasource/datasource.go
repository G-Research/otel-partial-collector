package datasource

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

type PartialSpan struct {
	Resource *resourcev1.Resource
	Scope    *commonv1.InstrumentationScope
	Span     *tracev1.Span
}

type DataSource interface {
	PutTraces(ctx context.Context, traces ptrace.Traces) error
	RemoveTraces(ctx context.Context, traces ptrace.Traces) error
	GetTracesOlderThan(ctx context.Context, timestamp time.Time) ([]ptrace.Traces, error)
	Close(ctx context.Context) error
}

func FlattenTraces(traces ptrace.Traces) []ptrace.Traces {
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
