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
	Trace []byte
}

func (db *DB) PutTrace(ctx context.Context, partialTrace *PartialTrace) error {
	q := `
INSERT INTO partial_traces
(trace_id, span_id, trace, timestamp)
VALUES
($1, $2, $3, NOW())
ON CONFLICT (trace_id, span_id) DO UPDATE
SET trace = $3, timestamp = NOW()
`

	if _, err := db.Exec(
		ctx,
		q,
		partialTrace.TraceID,
		partialTrace.SpanID,
		partialTrace.Trace,
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

func (db *DB) GetTracesOlderThan(ctx context.Context, timestamp time.Time) ([]*PartialTrace, error) {
	q := `
SELECT trace_id, span_id, trace FROM partial_traces
WHERE timestamp < $1
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
