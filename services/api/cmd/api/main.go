// Command api is the public HTTP entry point: authentication, tenant resolution, REST/SSE and command forwarding.
// Phase 02 provides the server shell and health surface; product endpoints land in Phase 03.
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/cors"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/platform/buildinfo"
	"github.com/agent-platform/agent-platform/internal/platform/config"
	"github.com/agent-platform/agent-platform/internal/platform/httpserver"
	"github.com/agent-platform/agent-platform/internal/platform/logging"
	"github.com/agent-platform/agent-platform/internal/platform/postgres"
	"github.com/agent-platform/agent-platform/internal/platform/run"
)

type serviceConfig struct {
	config.Common

	HTTPAddr    string   `env:"API_HTTP_ADDR" envDefault:":8080"`
	DatabaseURL string   `env:"API_DATABASE_URL" envDefault:"postgres://app_api:dev_api_password@localhost:5432/agent_platform?sslmode=disable"`
	CORSOrigins []string `env:"API_CORS_ORIGINS" envSeparator:"," envDefault:"http://localhost:3000"`
}

func main() {
	cfg, err := config.Load[serviceConfig]()
	if err != nil {
		panic(err)
	}

	log, err := logging.New(logging.Config{
		Service:     "api",
		Environment: cfg.Environment,
		Level:       cfg.LogLevel,
		Format:      cfg.LogFormat,
		Version:     buildinfo.Version,
	})
	if err != nil {
		panic(err)
	}
	defer func() { _ = log.Sync() }()

	ctx := run.SignalContext()

	pool, err := postgres.Connect(ctx, postgres.Config{URL: cfg.DatabaseURL, MaxConns: 20, Logger: log})
	if err != nil {
		log.Fatal("database unavailable", zap.Error(err))
	}
	defer pool.Close()

	dbCheck := func(ctx context.Context) error { return pool.Ping(ctx) }

	srv := httpserver.New(httpserver.Options{
		Addr:   cfg.HTTPAddr,
		Logger: log,
		Middlewares: []func(http.Handler) http.Handler{
			cors.Handler(cors.Options{
				AllowedOrigins:   cfg.CORSOrigins,
				AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodOptions},
				AllowedHeaders:   []string{"Authorization", "Content-Type", "Idempotency-Key", "Last-Event-ID"},
				AllowCredentials: true,
				MaxAge:           300,
			}),
		},
		Checks: []httpserver.Check{dbCheck},
	})
	srv.Router().Get("/v1/meta", metaHandler(cfg.Environment))

	log.Info("starting api", zap.String("commit", buildinfo.Commit))

	if err := run.Group(ctx, srv.Start); err != nil {
		log.Fatal("api stopped", zap.Error(err))
	}
	log.Info("api stopped cleanly")
}

func metaHandler(environment string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{
			"name":        "agent-platform-api",
			"environment": environment,
			"version":     buildinfo.Version,
			"commit":      buildinfo.Commit,
			"build_date":  buildinfo.BuildDate,
			"time":        time.Now().UTC().Format(time.RFC3339),
		})
	}
}
