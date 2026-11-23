// Command orchestrator owns all execution state transitions: materialisation, scheduling, leases, retries,
// completion and event publishing. Phase 02 wires the process, its dependencies and the gRPC contract; Phase 03
// implements the command handlers and scheduler.
package main

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"

	orchestratorv1 "github.com/agent-platform/agent-platform/gen/orchestrator/v1"
	"github.com/agent-platform/agent-platform/internal/platform/buildinfo"
	"github.com/agent-platform/agent-platform/internal/platform/config"
	"github.com/agent-platform/agent-platform/internal/platform/grpcserver"
	"github.com/agent-platform/agent-platform/internal/platform/httpserver"
	"github.com/agent-platform/agent-platform/internal/platform/logging"
	"github.com/agent-platform/agent-platform/internal/platform/natsconn"
	"github.com/agent-platform/agent-platform/internal/platform/postgres"
	"github.com/agent-platform/agent-platform/internal/platform/run"
	"github.com/agent-platform/agent-platform/services/orchestrator/internal/commands"
)

type serviceConfig struct {
	config.Common

	GRPCAddr    string `env:"ORCHESTRATOR_GRPC_ADDR" envDefault:":9090"`
	HTTPAddr    string `env:"ORCHESTRATOR_HTTP_ADDR" envDefault:":8081"`
	DatabaseURL string `env:"ORCHESTRATOR_DATABASE_URL" envDefault:"postgres://app_orchestrator:dev_orchestrator_password@localhost:5432/agent_platform?sslmode=disable"`
	NATSURL     string `env:"ORCHESTRATOR_NATS_URL" envDefault:"nats://localhost:4222"`

	MaxActiveExecutions int `env:"ORCHESTRATOR_MAX_ACTIVE_EXECUTIONS" envDefault:"20"`
	MaxQueuedExecutions int `env:"ORCHESTRATOR_MAX_QUEUED_EXECUTIONS" envDefault:"100"`
	MaxExecutionSeconds int `env:"ORCHESTRATOR_MAX_EXECUTION_SECONDS" envDefault:"86400"`
}

func main() {
	cfg, err := config.Load[serviceConfig]()
	if err != nil {
		panic(err)
	}

	log, err := logging.New(logging.Config{
		Service:     "orchestrator",
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

	nc, err := natsconn.Connect(ctx, cfg.NATSURL, "orchestrator")
	if err != nil {
		log.Fatal("nats unavailable", zap.Error(err))
	}
	defer nc.Close()

	js, err := natsconn.JetStream(nc)
	if err != nil {
		log.Fatal("jetstream unavailable", zap.Error(err))
	}
	account, err := js.AccountInfo(ctx)
	if err != nil {
		log.Fatal("jetstream account unavailable", zap.Error(err))
	}
	log.Info("jetstream ready", zap.Int("streams", account.Streams), zap.Int("consumers", account.Consumers))

	// The API calls this service. Unimplemented handlers return codes.Unimplemented until Phase 03.
	grpcSrv, err := grpcserver.New(cfg.GRPCAddr, cfg.Environment, log)
	if err != nil {
		log.Fatal("grpc listen failed", zap.Error(err))
	}
	orchestratorv1.RegisterExecutionCommandsServer(grpcSrv.Server, commands.New(log, pool, commands.Quotas{
		MaxActive:            cfg.MaxActiveExecutions,
		MaxQueued:            cfg.MaxQueuedExecutions,
		MaxExecutionDuration: time.Duration(cfg.MaxExecutionSeconds) * time.Second,
	}))

	httpSrv := httpserver.New(httpserver.Options{
		Addr:   cfg.HTTPAddr,
		Logger: log,
		Checks: []httpserver.Check{
			func(ctx context.Context) error { return pool.Ping(ctx) },
			natsconn.HealthCheck(nc),
		},
	})
	httpSrv.Router().Get("/v1/version", func(w http.ResponseWriter, _ *http.Request) {
		httpserver.WriteJSON(w, http.StatusOK, buildinfo.Info())
	})

	log.Info("starting orchestrator")

	if err := run.Group(ctx, grpcSrv.Start, httpSrv.Start); err != nil {
		log.Fatal("orchestrator stopped", zap.Error(err))
	}
	log.Info("orchestrator stopped cleanly")
}
