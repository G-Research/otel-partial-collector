package otelpartialreceiver

import (
	"errors"
	"time"

	"go.opentelemetry.io/collector/component"
)

type Config struct {
	// Postgres is URL used to connect to the postgres instance.
	Postgres string `mapstructure:"postgres"`

	// GCInterval is the time to wait between GC runs.
	GCInterval time.Duration `mapstructure:"gc_interval"`

	// BatchMaxSize is the maximum amount of partial traces to GC in one go.
	// If set to 0, no limit is applied.
	BatchMaxSize int64 `mapstructure:"batch_max_size"`
}

func (c *Config) Validate() error {
	if c.GCInterval < 0 {
		return errors.New("'gc_interval' must be non-negative")
	}
	if c.BatchMaxSize < 0 {
		return errors.New("'batch_max_size' must be non-negative")
	}
	return nil
}

func createDefaultConfig() component.Config {
	return &Config{
		GCInterval: 5 * time.Second,
	}
}
