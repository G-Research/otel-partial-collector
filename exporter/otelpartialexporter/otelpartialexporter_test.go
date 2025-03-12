package otelpartialexporter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

func TestMergeAttributes(t *testing.T) {
	dst := pcommon.NewMap()
	dst.PutStr("stays", "no_override")

	src := pcommon.NewMap()
	src.PutStr("stays", "override")
	src.PutBool("partial.ignored", true)
	src.PutInt("inherited", 7)

	mergeAttributes(dst, src)

	val, ok := dst.Get("stays")
	assert.True(t, ok)
	assert.Equal(t, "no_override", val.AsString())

	_, ok = dst.Get("partial.ignored")
	assert.False(t, ok)

	val, ok = dst.Get("inherited")
	assert.True(t, ok)
	assert.Equal(t, int64(7), val.Int())
}
