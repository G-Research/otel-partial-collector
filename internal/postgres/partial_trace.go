package postgres

import (
	"context"
	"fmt"
	"time"
)

type PartialTrace struct {
	TraceID string
	SpanID  string
	// Marshalled trace to byte slice
	Trace     []byte
	Timestamp time.Time
	ExpiresAt time.Time
}

func (db *DB) PutTrace(ctx context.Context, partialTrace *PartialTrace) error {
	q := `
INSERT INTO partial_traces
(trace_id, span_id, trace, timestamp, expires_at)
VALUES
($1, $2, $3, $4, $5)
ON CONFLICT (trace_id, span_id) DO UPDATE
SET trace = $3, timestamp = $4, expires_at = $5
`

	if _, err := db.Exec(
		ctx,
		q,
		partialTrace.TraceID,
		partialTrace.SpanID,
		partialTrace.Trace,
		partialTrace.Timestamp,
		partialTrace.ExpiresAt,
	); err != nil {
		return fmt.Errorf("failed to insert partial span: %w", err)
	}

	return nil
}

func (db *DB) RemoveTrace(ctx context.Context, traceID, spanID string) error {
	q := `
DELETE FROM partial_traces
WHERE trace_id = $1 AND span_id = $2
	`

	if _, err := db.Exec(ctx, q, traceID, spanID); err != nil {
		return fmt.Errorf("failed to delete partial span: %w", err)
	}

	return nil
}

func (db *DB) ListExpiredTraces(ctx context.Context, timestamp time.Time) ([]*PartialTrace, error) {
	q := `
SELECT trace_id, span_id, trace FROM partial_traces
WHERE expires_at < $1
FOR UPDATE
	`

	rows, err := db.Query(ctx, q, timestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to query traces")
	}
	defer rows.Close()

	var traces []*PartialTrace
	for rows.Next() {
		var trace PartialTrace
		if err := rows.Scan(&trace.TraceID, &trace.SpanID, &trace.Trace); err != nil {
			return nil, fmt.Errorf("failed to scan row: %v", err)
		}
		traces = append(traces, &trace)
	}

	return traces, nil
}
