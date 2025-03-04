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
