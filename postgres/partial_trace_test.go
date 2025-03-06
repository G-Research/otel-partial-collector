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
	partialTrace := generatePartialTrace(t)

	err := ts.tp.db.PutTrace(context.Background(), partialTrace)
	require.NoError(t, err, "failed to put the first trace")

	err = ts.tp.db.PutTrace(context.Background(), partialTrace)
	require.NoError(t, err, "repeated put should succeed")

	rows, err := ts.tp.db.Query(
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
