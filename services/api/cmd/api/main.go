// Command api is the public HTTP entry point: authentication, tenant resolution, REST and command forwarding.
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
	"github.com/agent-platform/agent-platform/internal/platform/secretbox"
	"github.com/agent-platform/agent-platform/internal/platform/telemetry"
	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
	"github.com/agent-platform/agent-platform/services/api/internal/executions"
	"github.com/agent-platform/agent-platform/services/api/internal/secrets"
	"github.com/agent-platform/agent-platform/services/api/internal/server"
)

type serviceConfig struct {
	config.Common

	HTTPAddr    string   `env:"API_HTTP_ADDR" envDefault:":8080"`
	DatabaseURL string   `env:"API_DATABASE_URL" envDefault:"postgres://app_api:dev_api_password@localhost:5432/agent_platform?sslmode=disable"`
	CORSOrigins []string `env:"API_CORS_ORIGINS" envSeparator:"," envDefault:"http://localhost:3000"`

	OrchestratorAddr     string `env:"API_ORCHESTRATOR_GRPC_ADDR" envDefault:"localhost:9090"`
	OrchestratorInsecure bool   `env:"API_ORCHESTRATOR_INSECURE" envDefault:"true"`
	OrchestratorToken    string `env:"API_ORCHESTRATOR_TOKEN"`

	AuthMode      string `env:"AUTH_MODE" envDefault:""`
	AuthDevSecret string `env:"AUTH_DEV_SECRET"`
	AuthDevIssuer string `env:"AUTH_DEV_ISSUER" envDefault:"agent-platform-dev"`
	AuthAudience  string `env:"AUTH_AUDIENCE" envDefault:"agent-platform-api"`
	OIDCIssuerURL string `env:"OIDC_ISSUER_URL"`
	OIDCClientID  string `env:"OIDC_CLIENT_ID"`

	RateLimitPerMinute int `env:"API_RATE_LIMIT_PER_MINUTE" envDefault:"600"`
	RateLimitBurst     int `env:"API_RATE_LIMIT_BURST" envDefault:"60"`

	SecretsMasterKey    string `env:"SECRETS_MASTER_KEY"`
	SecretsActiveKeyID  string `env:"SECRETS_ACTIVE_KEY_ID" envDefault:"primary"`
	SecretsPreviousKeys string `env:"SECRETS_PREVIOUS_KEYS"`
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
	if cfg.Environment != "development" {
		if cfg.SecretsMasterKey == "" || cfg.SecretsMasterKey == "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" {
			log.Fatal("SECRETS_MASTER_KEY must be replaced outside development")
		}
		if cfg.OrchestratorToken == "" {
			log.Fatal("API_ORCHESTRATOR_TOKEN is required outside development")
		}
	}

	ctx := run.SignalContext()
	shutdownTelemetry, err := telemetry.Init(ctx, "api", cfg.Environment, buildinfo.Version)
	if err != nil {
		log.Fatal("telemetry configuration failed", zap.Error(err))
	}
	defer func() { _ = shutdownTelemetry(context.Background()) }()

	pool, err := postgres.Connect(ctx, postgres.Config{URL: cfg.DatabaseURL, MaxConns: 20, Logger: log})
	if err != nil {
		log.Fatal("database unavailable", zap.Error(err))
	}
	defer pool.Close()

	authConfig := auth.Config{
		Mode:          cfg.AuthMode,
		Environment:   cfg.Environment,
		DevSecret:     cfg.AuthDevSecret,
		DevIssuer:     cfg.AuthDevIssuer,
		Audience:      cfg.AuthAudience,
		OIDCIssuerURL: cfg.OIDCIssuerURL,
		OIDCClientID:  cfg.OIDCClientID,
	}
	verifier, err := auth.NewVerifier(ctx, authConfig)
	if err != nil {
		log.Fatal("invalid authentication configuration", zap.Error(err))
	}
	verifier = auth.NewAPIKeyVerifier(pool, verifier)

	var devIssuer *auth.DevIssuer
	if verifier.IsDevelopment() {
		devIssuer, err = auth.NewDevIssuer(authConfig)
		if err != nil {
			log.Fatal("invalid development auth configuration", zap.Error(err))
		}
		log.Warn("development bearer token endpoint is mounted at POST /v1/dev/token")
	} else {
		log.Info("OIDC bearer authentication enabled")
	}

	orchestrator, err := executions.DialOrchestrator(cfg.OrchestratorAddr, cfg.OrchestratorInsecure,
		cfg.OrchestratorToken, log)
	if err != nil {
		log.Fatal("orchestrator client configuration failed", zap.Error(err))
	}
	defer func() { _ = orchestrator.Close() }()

	db := database.New(pool)
	var keyring *secretbox.Keyring
	if cfg.SecretsMasterKey != "" {
		keyring, err = secretbox.New(cfg.SecretsActiveKeyID, cfg.SecretsMasterKey, cfg.SecretsPreviousKeys)
		if err != nil {
			log.Fatal("invalid secret encryption configuration", zap.Error(err))
		}
	} else {
		log.Warn("secret store is not configured; credential endpoints return 503")
	}
	secretStore := secrets.NewStore(db, keyring)
	srv := httpserver.New(httpserver.Options{
		Addr:    cfg.HTTPAddr,
		Service: "api",
		Logger:  log,
		Middlewares: []func(http.Handler) http.Handler{
			cors.Handler(cors.Options{
				AllowedOrigins:   cfg.CORSOrigins,
				AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodOptions},
				AllowedHeaders:   []string{"Authorization", "X-API-Key", "Content-Type", "Idempotency-Key", "Last-Event-ID", "Traceparent"},
				AllowCredentials: true,
				MaxAge:           300,
			}),
		},
		Checks: []httpserver.Check{func(ctx context.Context) error { return pool.Ping(ctx) }},
	})
	srv.Router().Get("/v1/meta", metaHandler(cfg.Environment))
	srv.Router().Get("/api/v1/meta", metaHandler(cfg.Environment))

	server.Mount(srv.Router(), server.Deps{
		DB:           db,
		Verifier:     verifier,
		DevIssuer:    devIssuer,
		Orchestrator: orchestrator,
		Secrets:      secrets.NewHandlers(secretStore, db),
		Logger:       log,
		Config: server.Config{
			RateLimitPerMinute: cfg.RateLimitPerMinute,
			RateLimitBurst:     cfg.RateLimitBurst,
			MaxBodyBytes:       1 << 20,
		},
	})

	log.Info("starting api", zap.String("commit", buildinfo.Commit), zap.String("auth_mode", cfg.AuthMode))

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
