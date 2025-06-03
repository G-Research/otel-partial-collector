package otelpartialexporter

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/config/configretry"
	"go.opentelemetry.io/collector/confmap/confmaptest"
	"go.opentelemetry.io/collector/confmap/xconfmap"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()
	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, err)

	want := &Config{
		Postgres:     "postgres://postgres:test@127.0.0.1:40444/otelpartialcollector?sslmode=disable",
		ExpiryFactor: 3,
		QueueConfig:  exporterhelper.NewDefaultQueueConfig(),
		RetryConfig:  configretry.NewDefaultBackOffConfig(),
	}
	want.QueueConfig.Sizer = exporterhelper.RequestSizerTypeItems

	got := createDefaultConfig().(*Config)
	sub, err := cm.Sub(typeStr.String())
	require.NoError(t, err)
	require.NoError(t, sub.Unmarshal(got))

	assert.NoError(t, xconfmap.Validate(got))
	assert.Equal(t, want, got)
}
