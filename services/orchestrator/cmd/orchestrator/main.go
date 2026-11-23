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
	workerv1 "github.com/agent-platform/agent-platform/gen/worker/v1"
	"github.com/agent-platform/agent-platform/internal/messaging"
	"github.com/agent-platform/agent-platform/internal/outbox"
	"github.com/agent-platform/agent-platform/internal/platform/buildinfo"
	"github.com/agent-platform/agent-platform/internal/platform/config"
	"github.com/agent-platform/agent-platform/internal/platform/grpcserver"
	"github.com/agent-platform/agent-platform/internal/platform/httpserver"
	"github.com/agent-platform/agent-platform/internal/platform/logging"
	"github.com/agent-platform/agent-platform/internal/platform/natsconn"
	"github.com/agent-platform/agent-platform/internal/platform/postgres"
	"github.com/agent-platform/agent-platform/internal/platform/run"
	"github.com/agent-platform/agent-platform/services/orchestrator/internal/admin"
	"github.com/agent-platform/agent-platform/services/orchestrator/internal/commands"
	"github.com/agent-platform/agent-platform/services/orchestrator/internal/lifecycle"
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

	// ArtifactDir offloads checkpoints above the inline threshold. Empty keeps all state in PostgreSQL.
	ArtifactDir string `env:"ORCHESTRATOR_ARTIFACT_DIR" envDefault:"./data/artifacts"`

	StreamReplicas    int    `env:"ORCHESTRATOR_STREAM_REPLICAS" envDefault:"1"`
	AdminToken        string `env:"ORCHESTRATOR_ADMIN_TOKEN"`
	DispatcherMillis  int    `env:"ORCHESTRATOR_DISPATCH_INTERVAL_MS" envDefault:"500"`
	ReaperMillis      int    `env:"ORCHESTRATOR_REAPER_INTERVAL_MS" envDefault:"2000"`
	PublisherMillis   int    `env:"ORCHESTRATOR_OUTBOX_INTERVAL_MS" envDefault:"250"`
	PublisherBatch    int    `env:"ORCHESTRATOR_OUTBOX_BATCH" envDefault:"50"`
	PublisherRetrySec int    `env:"ORCHESTRATOR_OUTBOX_RETRY_SECONDS" envDefault:"5"`

	LeaseSeconds            int    `env:"ORCHESTRATOR_LEASE_SECONDS" envDefault:"30"`
	HeartbeatTimeoutSeconds int    `env:"ORCHESTRATOR_HEARTBEAT_TIMEOUT_SECONDS" envDefault:"15"`
	WorkerTerminateSeconds  int    `env:"ORCHESTRATOR_WORKER_TERMINATE_SECONDS" envDefault:"60"`
	MaxActiveTasksPerTenant int    `env:"ORCHESTRATOR_MAX_ACTIVE_TASKS_PER_TENANT" envDefault:"0"`
	ProviderConcurrency     string `env:"ORCHESTRATOR_PROVIDER_CONCURRENCY"`
	ToolConcurrency         string `env:"ORCHESTRATOR_TOOL_CONCURRENCY"`
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

	if err := messaging.EnsureStreams(ctx, js, messaging.StreamsConfig{Replicas: cfg.StreamReplicas}, log); err != nil {
		log.Fatal("nats streams unavailable", zap.Error(err))
	}

	grpcSrv, err := grpcserver.New(cfg.GRPCAddr, cfg.Environment, log)
	if err != nil {
		log.Fatal("grpc listen failed", zap.Error(err))
	}
	providerLimits, err := lifecycle.ParseConcurrencyLimits(cfg.ProviderConcurrency)
	if err != nil {
		log.Fatal("invalid provider concurrency limits", zap.Error(err))
	}
	toolLimits, err := lifecycle.ParseConcurrencyLimits(cfg.ToolConcurrency)
	if err != nil {
		log.Fatal("invalid tool concurrency limits", zap.Error(err))
	}
	lifecycleService := lifecycle.NewWithConfig(pool, log, lifecycle.Config{
		LeaseTTL:                time.Duration(cfg.LeaseSeconds) * time.Second,
		HeartbeatTimeout:        time.Duration(cfg.HeartbeatTimeoutSeconds) * time.Second,
		WorkerTerminateAfter:    time.Duration(cfg.WorkerTerminateSeconds) * time.Second,
		MaxActiveTasksPerTenant: cfg.MaxActiveTasksPerTenant,
		ProviderConcurrency:     providerLimits,
		ToolConcurrency:         toolLimits,
		ArtifactDir:             cfg.ArtifactDir,
	})
	orchestratorv1.RegisterExecutionCommandsServer(grpcSrv.Server, commands.New(log, pool, commands.Quotas{
		MaxActive:            cfg.MaxActiveExecutions,
		MaxQueued:            cfg.MaxQueuedExecutions,
		MaxExecutionDuration: time.Duration(cfg.MaxExecutionSeconds) * time.Second,
	}, lifecycleService))

	workerv1.RegisterWorkerControlServer(grpcSrv.Server, lifecycle.NewWorkerServer(lifecycleService, log))
	publisher := messaging.NewPublisherLoop(outbox.NewStore(pool),
		messaging.NewNATSPublisher(js, log), log,
		time.Duration(cfg.PublisherMillis)*time.Millisecond,
		cfg.PublisherBatch,
		time.Duration(cfg.PublisherRetrySec)*time.Second,
	)
	adminHandler := admin.New(lifecycleService, js, cfg.AdminToken, log)

	if err := lifecycleService.ReconcileWorkerSlots(ctx); err != nil {
		log.Warn("worker slot reconciliation failed", zap.Error(err))
	}

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
	adminHandler.Routes(httpSrv.Router())

	log.Info("starting orchestrator",
		zap.Int("stream_replicas", cfg.StreamReplicas),
		zap.Bool("operator_api", cfg.AdminToken != ""),
		zap.Duration("lease_ttl", time.Duration(cfg.LeaseSeconds)*time.Second),
		zap.Int("max_active_tasks_per_tenant", cfg.MaxActiveTasksPerTenant),
		zap.Int("provider_limits", len(providerLimits)),
		zap.Int("tool_limits", len(toolLimits)),
	)

	if err := run.Group(ctx,
		grpcSrv.Start,
		httpSrv.Start,
		publisher.Run,
		func(ctx context.Context) error {
			return lifecycleService.RunDispatcher(ctx, time.Duration(cfg.DispatcherMillis)*time.Millisecond)
		},
		func(ctx context.Context) error {
			return lifecycleService.RunRecovery(ctx, time.Duration(cfg.ReaperMillis)*time.Millisecond)
		},
	); err != nil {
		log.Fatal("orchestrator stopped", zap.Error(err))
	}
	log.Info("orchestrator stopped cleanly")
}
