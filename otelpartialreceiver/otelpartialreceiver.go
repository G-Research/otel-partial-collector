package otelpartialreceiver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/G-Research/otel-partial-connector/postgres"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

var typeStr = component.MustNewType("otelpartialreceiver")

var (
	logsJSONMarshaler      plog.JSONMarshaler
	tracesProtoUnmarshaler ptrace.ProtoUnmarshaler
	tracesProtoMarshaler   ptrace.ProtoMarshaler
)

type Config struct {
	Postgres string `mapstructure:"postgres"`
	Interval string `mapstructure:"interval"`
}

func (c *Config) Validate() error {
	if _, err := time.ParseDuration(c.Interval); err != nil {
		return fmt.Errorf("failed to parse interval duration: %v", err)
	}
	return nil
}

func defaultConfig() component.Config {
	return &Config{
		Interval: "24h",
	}
}

type otelPartialReceiver struct {
	consumer consumer.Traces
	db       *postgres.DB
	interval time.Duration
	host     component.Host

	logger *zap.Logger

	cancelFunc context.CancelFunc
}

func (r *otelPartialReceiver) Start(ctx context.Context, host component.Host) error {
	ctx, cancel := context.WithCancel(ctx)
	r.cancelFunc = cancel
	r.host = host

	go r.loop(ctx)
	return nil
}

func (r *otelPartialReceiver) Shutdown(ctx context.Context) error {
	if r.cancelFunc != nil {
		r.cancelFunc()
	}
	return r.db.Close(ctx)
}

func (r *otelPartialReceiver) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

func (c *otelPartialReceiver) gc(ctx context.Context) error {
	targetTimestamp := time.Now().Add(-30 * time.Minute) // todo: configurable
	return c.db.Transact(
		ctx,
		pgx.TxOptions{
			IsoLevel:       pgx.Serializable,
			AccessMode:     pgx.ReadWrite,
			DeferrableMode: pgx.NotDeferrable,
		},
		func(ctx context.Context, db *postgres.DB) error {
			traces, err := c.db.GetTracesOlderThan(ctx, targetTimestamp)
			if err != nil {
				return fmt.Errorf("failed to get traces older than %v: %v", targetTimestamp, err)
			}

			var errs []error
			for _, b := range traces {
				trace, err := tracesProtoUnmarshaler.UnmarshalTraces(b)
				if err != nil {
					errs = append(errs, fmt.Errorf("failed to unmarshal traces: %w", err))
					continue
				}

				span := trace.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
				span.SetEndTimestamp(pcommon.NewTimestampFromTime(time.Now()))

				if err := c.consumer.ConsumeTraces(ctx, trace); err != nil {
					errs = append(errs, fmt.Errorf("failed to consume trace %v: %w", trace, err))
					continue
				}

				if err := db.RemoveTrace(ctx, span.TraceID().String(), span.SpanID().String()); err != nil {
					errs = append(errs, fmt.Errorf("failed to rmeove trace: %v", err))
					continue
				}
			}

			return errors.Join(errs...)
		},
	)
}

func newPartialReceiver(ctx context.Context, params receiver.Settings, baseCfg component.Config, consumer consumer.Traces) (receiver.Traces, error) {
	cfg := baseCfg.(*Config)
	db, err := postgres.NewDB(ctx, cfg.Postgres)
	if err != nil {
		return nil, fmt.Errorf("failed to create new db connection: %v", err)
	}

	r := &otelPartialReceiver{
		db:     db,
		logger: params.Logger,
	}

	return r, nil
}

func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		typeStr,
		defaultConfig,
		receiver.WithTraces(
			newPartialReceiver,
			component.StabilityLevelAlpha,
		),
	)
}
