package otelpartialreceiver

import (
	"fmt"
	"time"

	"go.opentelemetry.io/collector/component"
)

type Config struct {
	Postgres   string `mapstructure:"postgres"`
	GCInterval string `mapstructure:"gc_interval"`
}

func (c *Config) Validate() error {
	if _, err := time.ParseDuration(c.GCInterval); err != nil {
		return fmt.Errorf("failed to parse interval duration: %w", err)
	}
	return nil
}

func createDefaultConfig() component.Config {
	return &Config{
		GCInterval: "5s",
	}
}
