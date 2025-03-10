package postgres_test

import (
	"context"

	"github.com/G-Research/otel-partial-connector/postgres"
	"github.com/stretchr/testify/require"
	"gotest.tools/v3/assert"
)

func (ts *TestSuite) TestCreateTrace() {
	ctx := context.Background()
	t := ts.T()
	db := ts.acquireDB()

	t.Cleanup(func() {
		_, err := db.Exec(context.Background(), "DELETE FROM partial_traces")
		t.Logf("failed to cleanup from partial_traces: %v", err)
		ts.releaseDB()
	})

	partialTrace := generatePartialTrace(t)

	err := db.PutTrace(context.Background(), partialTrace)
	require.NoError(t, err, "failed to put the first trace")

	err = db.PutTrace(context.Background(), partialTrace)
	require.NoError(t, err, "repeated put should succeed")

	rows, err := db.Query(
		ctx,
		"SELECT trace_id, span_id, trace from partial_traces",
	)
	require.NoError(t, err)
	defer rows.Close()

	var got []*postgres.PartialTrace
	for rows.Next() {
		var pt postgres.PartialTrace
		err = rows.Scan(&pt.TraceID, &pt.SpanID, &pt.Trace)
		require.NoError(t, err)
		got = append(got, &pt)
	}

	assert.Equal(t, 1, len(got))
	assert.DeepEqual(t, partialTrace, got[0])
}

func (ts *TestSuite) TestDeleteTrace() {
	ctx := context.Background()
	t := ts.T()
	db := ts.acquireDB()

	t.Cleanup(func() {
		_, err := db.Exec(context.Background(), "DELETE FROM partial_traces")
		t.Logf("failed to cleanup from partial_traces: %v", err)
		ts.releaseDB()
	})

	partialTrace := generatePartialTrace(t)

	err := db.PutTrace(context.Background(), partialTrace)
	require.NoError(t, err, "failed to put the first trace")

	err = db.RemoveTrace(ctx, partialTrace.TraceID, partialTrace.SpanID)
	require.NoError(t, err)

	var count int
	err = db.QueryRow(ctx, "SELECT COUNT(*) FROM partial_traces").Scan(&count)
	require.NoError(t, err)

	assert.Equal(t, 0, count)
}
