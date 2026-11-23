// Package config loads service configuration from the environment.
//
// Precedence is deliberately simple: environment variable, then the tag default. There are no config files
// because every deployable is configured the same way in Docker Compose, in CI and in production.
package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
)

// Common is embedded by every service config.
type Common struct {
	Environment string `env:"ENVIRONMENT" envDefault:"development"`
	LogLevel    string `env:"LOG_LEVEL" envDefault:"info"`
	LogFormat   string `env:"LOG_FORMAT" envDefault:"json"` // json | console
}

// Load parses cfg from the environment. It fails loudly on malformed values; configuration is not optional.
func Load[T any]() (T, error) {
	var cfg T
	if err := env.Parse(&cfg); err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
