// Package logging builds the one logger every service uses.
//
// Logs are JSON in every environment except explicit local console use. Every line carries service, environment
// and version fields so log stores can separate deployables without parsing the message.
package logging

import (
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config is the subset of service configuration that affects logging.
type Config struct {
	Service     string
	Environment string
	Level       string
	Format      string // json | console
	Version     string
}

// New returns a logger that is safe for concurrent use and flushes on process exit via Sync.
func New(cfg Config) (*zap.Logger, error) {
	level := zapcore.InfoLevel
	if err := level.UnmarshalText([]byte(strings.ToLower(cfg.Level))); err != nil {
		return nil, err
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	var encoder zapcore.Encoder
	if cfg.Format == "console" {
		encoderCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoder = zapcore.NewConsoleEncoder(encoderCfg)
	} else {
		encoder = zapcore.NewJSONEncoder(encoderCfg)
	}

	core := zapcore.NewCore(encoder, zapcore.Lock(os.Stdout), level)
	return zap.New(core).With(
		zap.String("service", cfg.Service),
		zap.String("environment", cfg.Environment),
		zap.String("version", cfg.Version),
	), nil
}
