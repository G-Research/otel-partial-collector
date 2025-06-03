package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type PartialTraceKey struct {
	TraceID string
	SpanID  string
}

type PartialTrace struct {
	PartialTraceKey
	// Marshaled trace to byte slice
	Trace     []byte
	Timestamp time.Time
	ExpiresAt time.Time
}

func (db *DB) PutTrace(ctx context.Context, trace *PartialTrace) error {
	return db.PutTraces(ctx, []*PartialTrace{trace})
}

func (db *DB) PutTraces(ctx context.Context, traces []*PartialTrace) error {
	if len(traces) == 0 {
		return nil
	}

	var (
		valuePlaceholders []string
		args              []any
	)

	for i, pt := range traces {
		// Generate: ($1, $2, $3, $4, $5), ($6, $7, $8, $9, $10), ...
		n := i * 5
		valuePlaceholders = append(valuePlaceholders,
			fmt.Sprintf("($%d, $%d, $%d, $%d, $%d)", n+1, n+2, n+3, n+4, n+5),
		)

		args = append(args,
			pt.TraceID,
			pt.SpanID,
			pt.Trace,
			pt.Timestamp,
			pt.ExpiresAt,
		)
	}

	q := fmt.Sprintf(`
INSERT INTO partial_traces
(trace_id, span_id, trace, timestamp, expires_at)
VALUES %s
ON CONFLICT (trace_id, span_id) DO UPDATE
SET trace = EXCLUDED.trace,
    timestamp = EXCLUDED.timestamp,
    expires_at = EXCLUDED.expires_at
`, strings.Join(valuePlaceholders, ", "))

	if _, err := db.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("failed to upsert partial spans: %w", err)
	}

	return nil
}

func (db *DB) RemoveTrace(ctx context.Context, traceID, spanID string) error {
	return db.RemoveTraces(ctx, []PartialTraceKey{{traceID, spanID}})
}

func (db *DB) RemoveTraces(ctx context.Context, traceKeys []PartialTraceKey) error {
	if len(traceKeys) == 0 {
		return nil
	}

	var (
		valuePlaceholders []string
		args              []any
	)

	for i, key := range traceKeys {
		n := i * 2
		valuePlaceholders = append(valuePlaceholders,
			fmt.Sprintf("($%d, $%d)", n+1, n+2),
		)
		args = append(args, key.TraceID, key.SpanID)
	}

	q := fmt.Sprintf(`
DELETE FROM partial_traces
WHERE (trace_id, span_id) IN (
  VALUES %s
)
`, strings.Join(valuePlaceholders, ", "))

	if _, err := db.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("failed to delete partial spans: %w", err)
	}

	return nil
}

func (db *DB) ListExpiredTraces(ctx context.Context, timestamp time.Time, limit int64) ([]*PartialTrace, error) {
	q := `
SELECT trace_id, span_id, trace FROM partial_traces
WHERE expires_at < $1
FOR UPDATE SKIP LOCKED
`

	if limit > 0 {
		q += fmt.Sprintf("LIMIT %d\n", limit)
	}

	rows, err := db.Query(ctx, q, timestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to query traces: %w", err)
	}
	defer rows.Close()

	var traces []*PartialTrace
	for rows.Next() {
		var trace PartialTrace
		if err := rows.Scan(&trace.TraceID, &trace.SpanID, &trace.Trace); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		traces = append(traces, &trace)
	}

	return traces, nil
}
