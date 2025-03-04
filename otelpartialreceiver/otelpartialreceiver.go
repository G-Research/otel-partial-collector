package otelpartialreceiver

import (
	"fmt"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/receiver"
)

type Config struct {
	Interval string `json:"interval"`
}

func (c *Config) Validate() error {
	if _, err := time.ParseDuration(c.Interval); err != nil {
		return fmt.Errorf("failed to parse interval duration: %v", err)
	}
	return nil
}

func defaultConfig() component.Config {
	return &Config{
		Interval: "24h",
	}
}

func NewFactory() receiver.Factory {
	return nil
}
