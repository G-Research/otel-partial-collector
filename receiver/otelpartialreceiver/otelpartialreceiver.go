package otelpartialreceiver

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/G-Research/otel-partial-collector/internal/postgres"
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
	Postgres    string `mapstructure:"postgres"`
	GCThreshold string `mapstructure:"gc_threshold"`
}

func (c *Config) Validate() error {
	if _, err := time.ParseDuration(c.GCThreshold); err != nil {
		return fmt.Errorf("failed to parse interval duration: %v", err)
	}
	return nil
}

func defaultConfig() component.Config {
	return &Config{
		GCThreshold: "24h",
	}
}

type otelPartialReceiver struct {
	consumer    consumer.Traces
	db          *postgres.DB
	gcThreshold time.Duration
	host        component.Host

	logger *zap.Logger

	cancelFunc context.CancelFunc
	doneCh     chan struct{}
}

func newPartialReceiver(ctx context.Context, params receiver.Settings, baseCfg component.Config, consumer consumer.Traces) (receiver.Traces, error) {
	cfg := baseCfg.(*Config)
	db, err := postgres.NewDB(ctx, cfg.Postgres)
	if err != nil {
		return nil, fmt.Errorf("failed to create new db connection: %v", err)
	}

	d, err := time.ParseDuration(cfg.GCThreshold)
	if err != nil {
		return nil, fmt.Errorf("failed to parse duration interval")
	}

	r := &otelPartialReceiver{
		db:          db,
		logger:      params.Logger,
		gcThreshold: d,
	}

	return r, nil
}

func (r *otelPartialReceiver) Start(rootCtx context.Context, host component.Host) error {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancelFunc = cancel
	r.doneCh = make(chan struct{})
	r.host = host

	r.logger.Info("Starting gc loop", zap.String("gc_threshold", r.gcThreshold.String()))
	go r.loop(ctx)

	return rootCtx.Err()
}

func (r *otelPartialReceiver) Shutdown(ctx context.Context) error {
	r.logger.Info("Shutting down receiver")
	if r.cancelFunc != nil {
		r.cancelFunc()
		r.logger.Info("Waiting on gc loop to finish")
		<-r.doneCh
		r.logger.Info("GC loop done")
	}
	return r.db.Close(ctx)
}

func (r *otelPartialReceiver) loop(ctx context.Context) {
	for {
		// between 30 and 50 seconds
		jitter := rand.Intn(20)
		select {
		case <-ctx.Done():
			r.logger.Info("Stopping gc loop after shutdown")
			close(r.doneCh)
			return
		case <-time.After(time.Duration(30+jitter) * time.Second):
			if err := r.gc(ctx); err != nil {
				r.logger.Error("encountered errors while running gc", zap.Error(err))
			}
		}
	}
}

func (c *otelPartialReceiver) gc(ctx context.Context) error {
	now := time.Now()
	targetTimestamp := now.Add(-c.gcThreshold)

	var errs []error
	if err := c.db.Transact(
		ctx,
		pgx.TxOptions{
			IsoLevel:       pgx.Serializable,
			AccessMode:     pgx.ReadWrite,
			DeferrableMode: pgx.NotDeferrable,
		},
		func(ctx context.Context, db *postgres.DB) error {
			c.logger.Info("Collecting traces", zap.String("older_than", targetTimestamp.String()))

			traces, err := db.GetTracesOlderThan(ctx, targetTimestamp)
			if err != nil {
				return fmt.Errorf("failed to get traces older than %v: %v", targetTimestamp, err)
			}

			c.logger.Info("Number of traces to collect", zap.Int("count", len(traces)))

			for _, pt := range traces {
				trace, err := tracesProtoUnmarshaler.UnmarshalTraces(pt.Trace)
				if err != nil {
					errs = append(errs, fmt.Errorf("failed to unmarshal traces: %w", err))
					continue
				}

				span := trace.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
				span.SetEndTimestamp(pcommon.NewTimestampFromTime(now))
				attrs := span.Attributes()
				attrs.PutBool("partial.gc", true)

				if err := c.consumer.ConsumeTraces(ctx, trace); err != nil {
					errs = append(errs, fmt.Errorf("failed to consume trace %v: %w", trace, err))
					continue
				}

				if err := db.RemoveTrace(ctx, pt.TraceID, pt.SpanID); err != nil {
					errs = append(errs, fmt.Errorf("failed to rmeove trace: %v", err))
					continue
				}
			}
			return nil
		},
	); err != nil {
		return fmt.Errorf("transaction errors %w: %w", errors.Join(errs...), err)
	}

	return errors.Join(errs...)
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
