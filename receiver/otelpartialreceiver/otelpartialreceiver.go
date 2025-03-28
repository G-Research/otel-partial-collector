package otelpartialreceiver

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/receiver"

	"github.com/G-Research/otel-partial-collector/internal/postgres"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

var typeStr = component.MustNewType("otelpartialreceiver")

var tracesProtoUnmarshaler ptrace.ProtoUnmarshaler

type otelPartialReceiver struct {
	consumer   consumer.Traces
	db         *postgres.DB
	gcInterval time.Duration
	host       component.Host

	logger *zap.Logger

	cancelFunc context.CancelFunc
	doneCh     chan struct{}
}

func newPartialReceiver(ctx context.Context, params receiver.Settings, baseCfg component.Config, consumer consumer.Traces) (receiver.Traces, error) {
	cfg := baseCfg.(*Config)
	db, err := postgres.NewDB(ctx, cfg.Postgres)
	if err != nil {
		return nil, fmt.Errorf("failed to create new db connection: %w", err)
	}

	d, err := time.ParseDuration(cfg.GCInterval)
	if err != nil {
		return nil, fmt.Errorf("failed to parse duration interval: %w", err)
	}

	r := &otelPartialReceiver{
		db:         db,
		logger:     params.Logger,
		gcInterval: d,
		consumer:   consumer,
	}

	return r, nil
}

func (r *otelPartialReceiver) Start(rootCtx context.Context, host component.Host) error {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancelFunc = cancel
	r.doneCh = make(chan struct{})
	r.host = host

	r.logger.Info("Starting gc loop", zap.String("gc_threshold", r.gcInterval.String()))
	go r.loop(ctx)

	return rootCtx.Err()
}

func (r *otelPartialReceiver) Shutdown(context.Context) error {
	r.logger.Info("Shutting down receiver")
	if r.cancelFunc != nil {
		r.cancelFunc()
		r.logger.Info("Waiting on gc loop to finish")
		<-r.doneCh
		r.logger.Info("GC loop done")
	}
	return r.db.Close()
}

func (r *otelPartialReceiver) loop(ctx context.Context) {
	for {
		jitter := time.Millisecond * time.Duration(rand.IntN(1000)-500) // [-500ms,499ms]
		select {
		case <-ctx.Done():
			r.logger.Info("Stopping gc loop after shutdown")
			close(r.doneCh)
			return
		case <-time.After(r.gcInterval + jitter):
			if err := r.gc(ctx); err != nil {
				r.logger.Error("encountered errors while running gc", zap.Error(err))
			}
		}
	}
}

func (r *otelPartialReceiver) gc(ctx context.Context) error {
	now := time.Now().UTC()
	var errs []error
	if err := r.db.Transact(
		ctx,
		pgx.TxOptions{
			IsoLevel:       pgx.Serializable,
			AccessMode:     pgx.ReadWrite,
			DeferrableMode: pgx.NotDeferrable,
		},
		func(ctx context.Context, db *postgres.DB) error {
			r.logger.Debug("Collecting traces")

			traces, err := db.ListExpiredTraces(ctx, now)
			if err != nil {
				return fmt.Errorf("failed to get expired traces: %w", err)
			}

			if len(traces) > 0 {
				r.logger.Info("Number of traces to collect", zap.Int("count", len(traces)))
			}

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

				if err := r.consumer.ConsumeTraces(ctx, trace); err != nil {
					errs = append(errs, fmt.Errorf("failed to consume trace %v: %w", trace, err))
					continue
				}

				if err := db.RemoveTrace(ctx, pt.TraceID, pt.SpanID); err != nil {
					errs = append(errs, fmt.Errorf("failed to rmeove trace: %w", err))
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
		createDefaultConfig,
		receiver.WithTraces(
			newPartialReceiver,
			component.StabilityLevelAlpha,
		),
	)
}
