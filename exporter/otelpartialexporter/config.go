package otelpartialexporter

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"go.opentelemetry.io/collector/component"
)

// Config defines configuration for otelpartialexporter.
type Config struct {
	// Postgres is URL used to connect to the postgres instance.
	Postgres string `mapstructure:"postgres"`
	// ExpiryFactor multiplies the heartbeat interval with the ExpiryFactor
	// to get the expiration time for the trace.
	ExpiryFactor int `mapstructure:"expiry_factor"`
}

func createDefaultConfig() component.Config {
	return &Config{}
}

func (c *Config) Validate() error {
	if _, err := pgx.ParseConfig(c.Postgres); err != nil {
		return fmt.Errorf("invalid postgres config: %w", err)
	}

	if c.ExpiryFactor <= 0 {
		return errors.New("expiry factor cannot be less than or equal to 0")
	}

	return nil
}
