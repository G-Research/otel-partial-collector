package otelpartialreceiver

import (
	"context"
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
	consumer     consumer.Traces
	db           *postgres.DB
	gcInterval   time.Duration
	batchMaxSize int64
	host         component.Host

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

	r := &otelPartialReceiver{
		db:           db,
		logger:       params.Logger,
		gcInterval:   cfg.GCInterval,
		batchMaxSize: cfg.BatchMaxSize,
		consumer:     consumer,
	}

	return r, nil
}

func (r *otelPartialReceiver) Start(rootCtx context.Context, host component.Host) error {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancelFunc = cancel
	r.doneCh = make(chan struct{})
	r.host = host

	r.logger.Info("Starting gc loop", zap.String("gc_interval", r.gcInterval.String()))
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
		jitter := time.Duration(rand.Int64N(int64(r.gcInterval/10*2))) - (r.gcInterval / 10) // [-10%,+10%]
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
	// Process expired traces in batch until none are left
	done := false
	for !done {
		if err := r.db.Transact(
			ctx,
			pgx.TxOptions{
				IsoLevel:       pgx.Serializable,
				AccessMode:     pgx.ReadWrite,
				DeferrableMode: pgx.NotDeferrable,
			},
			func(ctx context.Context, db *postgres.DB) error {
				now := time.Now().UTC()

				expiredTraces, err := db.ListExpiredTraces(ctx, now, r.batchMaxSize)
				if err != nil {
					return fmt.Errorf("failed to get expired traces: %w", err)
				}

				// We can exit once no expired traces are left
				if len(expiredTraces) == 0 {
					done = true
					return nil
				}

				toSend := ptrace.NewTraces()
				toDelete := make([]postgres.PartialTraceKey, len(expiredTraces))

				for i, pt := range expiredTraces {
					// Any expired trace will need to be deleted, even if unmarshalling failed
					toDelete[i] = postgres.PartialTraceKey{
						TraceID: pt.TraceID,
						SpanID:  pt.SpanID,
					}

					trace, err := tracesProtoUnmarshaler.UnmarshalTraces(pt.Trace)
					if err != nil {
						r.logger.Warn("Failed to unmarshal trace", zap.Error(err))
						continue
					}

					span := trace.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
					span.SetEndTimestamp(pcommon.NewTimestampFromTime(now))
					attrs := span.Attributes()
					attrs.PutBool("partial.gc", true)

					trace.ResourceSpans().MoveAndAppendTo(toSend.ResourceSpans())
				}

				if err := r.consumer.ConsumeTraces(ctx, toSend); err != nil {
					return fmt.Errorf("failed to consume traces %v: %w", toSend, err)
				}

				if err := db.RemoveTraces(ctx, toDelete); err != nil {
					return fmt.Errorf("failed to remove traces: %w", err)
				}

				return nil
			},
		); err != nil {
			return fmt.Errorf("transaction error: %w", err)
		}
	}

	return nil
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
