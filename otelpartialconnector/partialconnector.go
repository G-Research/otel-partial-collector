package otelpartialconnector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/G-Research/otel-partial-connector/otelpartialconnector/config"
	"github.com/G-Research/otel-partial-connector/otelpartialconnector/datasource"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/connector"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"go.uber.org/zap"
)

var cfgType = component.MustNewType("otelpartialconnector")

type logsToTracesConnector struct {
	tracesConsumer consumer.Traces
	logger         *zap.Logger
	datasource     datasource.DataSource
	gcOlderThan    time.Duration
	component.StartFunc
}

var (
	tracesProtoUnmarshaler ptrace.ProtoUnmarshaler
	tracesJSONMarshaler    ptrace.JSONMarshaler
	logsJSONMarshaler      plog.JSONMarshaler
)

func newConnector(ctx context.Context, params connector.Settings, cfg component.Config, nextConsumer consumer.Traces) (connector.Logs, error) {
	config := cfg.(*config.Config)
	datasource, err := config.DataSource(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate datasource: %v", err)
	}

	gcOlderThan, err := config.GCOlderThanDuration()
	if err != nil {
		return nil, fmt.Errorf("failed to parse duration: %v", err)
	}

	c := &logsToTracesConnector{
		tracesConsumer: nextConsumer,
		logger:         params.Logger,
		datasource:     datasource,
		gcOlderThan:    gcOlderThan,
	}

	go func() {
		ticker := time.NewTicker(1 * time.Minute)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.gc(ctx); err != nil {
					c.logger.Error("GC returned error", zap.Error(err))
				}
			}
		}
	}()

	return c, nil
}

func (c *logsToTracesConnector) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (c *logsToTracesConnector) Shutdown(ctx context.Context) error {
	return c.datasource.Close(ctx)
}

func (c *logsToTracesConnector) ConsumeLogs(ctx context.Context, logs plog.Logs) error {
	out, err := logsJSONMarshaler.MarshalLogs(logs)
	if err != nil {
		return err
	}
	c.logger.Debug("Consuming logs", zap.String("log", string(out)))

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

				out, err := tracesJSONMarshaler.MarshalTraces(traces)
				if err != nil {
					return err
				}

				switch val {
				case "heartbeat":
					c.logger.Info("Handling traces heartbeat", zap.String("traces", string(out)))
					if err := c.datasource.PutTraces(ctx, traces); err != nil {
						return fmt.Errorf("failed to put races: %v", err)
					}
				case "stop":
					c.logger.Info("Handling traces stop", zap.String("traces", string(out)))
					if err := c.datasource.RemoveTraces(ctx, traces); err != nil {
						return fmt.Errorf("failed to delete traces: %v", err)
					}
				default:
					c.logger.Error("Unknown attribute value", zap.String("partial.event", val))
				}
			}
		}
	}

	return nil
}

type Meta struct {
	Context  *SpanContext `json:"context"`
	ParentID string       `json:"parentId"`
}
type SpanContext struct {
	TraceID string `json:"traceId"`
	SpanID  string `json:"spanId"`
}

func NewFactory() connector.Factory {
	return connector.NewFactory(
		cfgType,
		config.Default,
		connector.WithLogsToTraces(
			newConnector,
			component.StabilityLevelDevelopment,
		),
	)
}

func (c *logsToTracesConnector) gc(ctx context.Context) error {
	targetTimestamp := time.Now().Add(-c.gcOlderThan)
	traces, err := c.datasource.GetTracesOlderThan(ctx, targetTimestamp)
	if err != nil {
		return fmt.Errorf("failed to get traces older than %v: %v", targetTimestamp, err)
	}

	var errs []error
	for _, trace := range traces {
		span := trace.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
		attrs := span.Attributes()
		attrs.PutBool("connector.gc", true)

		if err := c.tracesConsumer.ConsumeTraces(ctx, trace); err != nil {
			errs = append(errs, fmt.Errorf("failed to consume trace: %v", err))
			continue
		}

		if err := c.datasource.RemoveTraces(ctx, trace); err != nil {
			errs = append(errs, fmt.Errorf("failed to rmeove trace: %v", err))
			continue
		}
	}

	return errors.Join(errs...)
}
