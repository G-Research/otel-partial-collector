package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/G-Research/partialconnector/datasource"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

var _ datasource.DataSource = (*DB)(nil)

var (
	tracesProtoUnmarshaler ptrace.ProtoUnmarshaler
	tracesProtoMarshaler   ptrace.ProtoMarshaler
)

type traceKey struct {
	TraceID string
	SpanID  string
}

func (id *traceKey) Key() string {
	return fmt.Sprintf("trace:%s:span:%s", id.TraceID, id.SpanID)
}

type DB struct {
	pool *pgxpool.Pool

	// ephemeral when in tx
	tx pgx.Tx
}

func NewDB(ctx context.Context, conn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create new pgx pool: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping the database: %v", err)
	}

	return &DB{
		pool: pool,
	}, nil
}

func (db *DB) Close(ctx context.Context) error {
	if db.pool == nil {
		return fmt.Errorf("pool is nil")
	}
	db.pool.Close()
	return nil
}

func (db *DB) Transact(ctx context.Context, opts pgx.TxOptions, f func(ctx context.Context, db *DB) error) error {
	switch opts.IsoLevel {
	case pgx.Serializable, pgx.RepeatableRead:
		return db.transactWithRetry(ctx, opts, f)
	default:
		return db.transact(ctx, opts, f)
	}
}

func (db *DB) transactWithRetry(ctx context.Context, opts pgx.TxOptions, f func(ctx context.Context, db *DB) error) error {
	var errs []error
	for i := 0; i < 5; i++ {
		err := db.transact(ctx, opts, f)
		if err == nil {
			return nil
		}

		errs = append(errs, err)
		if !isSerializationError(err) {
			return errors.Join(errs...)
		}

		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("transaction failed after 5 retries: %w", errors.Join(errs...))
}

func isSerializationError(err error) bool {
	var gerr *pgconn.PgError
	if errors.As(err, &gerr) && gerr.Code == "40001" {
		return true
	}
	return false
}

func (db *DB) transact(ctx context.Context, opts pgx.TxOptions, f func(ctx context.Context, db *DB) error) error {
	var tx pgx.Tx
	var err error
	if db.tx != nil {
		tx, err = db.tx.Begin(ctx)
	} else {
		tx, err = db.pool.BeginTx(ctx, opts)
	}

	if err != nil {
		return err
	}

	defer tx.Rollback(ctx)

	err = f(ctx, &DB{tx: tx})
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (db *DB) Begin(ctx context.Context) (pgx.Tx, error) {
	if db.tx != nil {
		return db.tx.Begin(ctx)
	}
	return db.pool.Begin(ctx)
}

func (db *DB) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	if db.tx != nil {
		return db.tx.Exec(ctx, sql, arguments...)
	}
	return db.pool.Exec(ctx, sql, arguments...)
}

func (db *DB) Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error) {
	if db.tx != nil {
		return db.tx.Query(ctx, sql, arguments...)
	}
	return db.pool.Query(ctx, sql, arguments...)
}

func (db *DB) QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row {
	if db.tx != nil {
		return db.tx.QueryRow(ctx, sql, arguments...)
	}
	return db.pool.QueryRow(ctx, sql, arguments...)
}

func (db *DB) PutTraces(ctx context.Context, traces ptrace.Traces) error {
	if traces.SpanCount() == 1 {
		return db.putTrace(ctx, traces)
	}

	var errs []error
	for _, trace := range datasource.FlattenTraces(traces) {
		if err := db.putTrace(ctx, trace); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (db *DB) putTrace(ctx context.Context, trace ptrace.Traces) error {
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

	if _, err := db.Exec(ctx, q, traceKey.Key(), val); err != nil {
		return fmt.Errorf("failed to insert partial span: %w", err)
	}

	return nil
}

func (db *DB) RemoveTraces(ctx context.Context, traces ptrace.Traces) error {
	if traces.SpanCount() == 1 {
		return db.removeTrace(ctx, traces)
	}

	var errs []error
	for _, trace := range datasource.FlattenTraces(traces) {
		if err := db.removeTrace(ctx, trace); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (db *DB) removeTrace(ctx context.Context, trace ptrace.Traces) error {
	q := `
DELETE FROM partial_traces
WHERE key = $1
	`

	span := trace.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	traceKey := traceKey{
		TraceID: span.TraceID().String(),
		SpanID:  span.SpanID().String(),
	}

	if _, err := db.Exec(ctx, q, traceKey.Key()); err != nil {
		return fmt.Errorf("failed to delete partial span: %w", err)
	}

	return nil
}

func (db *DB) GetTracesOlderThan(ctx context.Context, timestamp time.Time) ([]ptrace.Traces, error) {
	q := `
SELECT value FROM partial_traces
WHERE timestamp < $1
	`

	rows, err := db.Query(ctx, q, timestamp)
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
