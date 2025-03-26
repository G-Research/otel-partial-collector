package otelpartialreceiver

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap/confmaptest"
	"go.opentelemetry.io/collector/confmap/xconfmap"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()
	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, err)

	want := &Config{
		Postgres:   "postgres://postgres:test@127.0.0.1:40444/otelpartialcollector?sslmode=disable",
		GCInterval: "10s",
	}

	got := createDefaultConfig().(*Config)
	sub, err := cm.Sub(typeStr.String())
	require.NoError(t, err)
	require.NoError(t, sub.Unmarshal(got))

	assert.NoError(t, xconfmap.Validate(got))
	assert.Equal(t, want, got)
}
