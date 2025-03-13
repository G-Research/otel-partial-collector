package otelpartialexporter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

func TestMergeAttributes(t *testing.T) {
	dst := pcommon.NewMap()
	dst.PutStr("stays", "no_override")

	src := pcommon.NewMap()
	// "stays is already applied on the destination, so it shouldn't be overwritten"
	src.PutStr("stays", "override")
	// "partial." should be ignored
	src.PutBool("partial.ignored", true)

	appliedPrimitiveAttributes := map[string]any{
		"applied.int":    int64(1),
		"applied.double": 1.1,
		"applied.bool":   true,
		"applied.str":    "str",
	}

	for k, v := range appliedPrimitiveAttributes {
		switch v := v.(type) {
		case int64:
			src.PutInt(k, v)
		case float64:
			src.PutDouble(k, v)
		case string:
			src.PutStr(k, v)
		case bool:
			src.PutBool(k, v)
		}
	}

	appliedMap := pcommon.NewMap()
	appliedMap.PutBool("applied.map.bool", true)
	{
		m := src.PutEmptyMap("applied.map")
		appliedMap.CopyTo(m)
	}

	appliedSlice := pcommon.NewSlice()
	err := appliedSlice.FromRaw([]any{1, 2, 3})
	require.NoError(t, err)
	{
		s := src.PutEmptySlice("applied.slice")
		appliedSlice.CopyTo(s)
	}

	appliedByteSlice := pcommon.NewByteSlice()
	appliedByteSlice.Append(1, 2, 3)
	{
		s := src.PutEmptyBytes("applied.bytes")
		appliedByteSlice.CopyTo(s)
	}

	src.PutEmpty("applied.empty")

	mergeAttributes(dst, src)

	val, ok := dst.Get("stays")
	assert.True(t, ok)
	assert.Equal(t, "no_override", val.AsString())

	_, ok = dst.Get("partial.ignored")
	assert.False(t, ok)

	for k, v := range appliedPrimitiveAttributes {
		val, ok := dst.Get(k)
		assert.True(t, ok)
		assert.Equal(t, v, val.AsRaw())
	}

	val, ok = dst.Get("applied.map")
	assert.True(t, ok)
	assert.Equal(t, appliedMap, val.Map())

	val, ok = dst.Get("applied.slice")
	assert.True(t, ok)
	assert.Equal(t, appliedSlice, val.Slice())

	val, ok = dst.Get("applied.bytes")
	assert.True(t, ok)
	assert.Equal(t, appliedByteSlice, val.Bytes())

	val, ok = dst.Get("applied.empty")
	assert.True(t, ok)
	assert.Equal(t, pcommon.ValueTypeEmpty, val.Type())
}
