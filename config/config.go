package config

import (
	"context"
	"fmt"
	"time"

	"github.com/G-Research/partialconnector/datasource"
	"github.com/G-Research/partialconnector/datasource/postgres"
	"go.opentelemetry.io/collector/component"
)

type Config struct {
	Postgres    string `mapstructure:"postgres"`
	GCOlderThan string `mapstructure:"gc_older_than"`
}

func (c *Config) Validate() error {
	return nil
}

func Default() component.Config {
	return &Config{
		GCOlderThan: "24h",
	}
}

func (c *Config) DataSource(ctx context.Context) (datasource.DataSource, error) {
	switch {
	case c.Postgres != "":
		return postgres.NewDB(ctx, c.Postgres)
	default:
		return nil, fmt.Errorf("no datasource configured") // for now
	}
}

func (c *Config) GCOlderThanDuration() (time.Duration, error) {
	return time.ParseDuration(c.GCOlderThan)
}
