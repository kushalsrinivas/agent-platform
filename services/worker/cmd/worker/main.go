// Command worker is the Go host that holds leases and runs one Python runtime process per attempt.
//
// It never writes PostgreSQL. Phase 02 wires configuration, NATS connectivity, identity and the health surface;
// the control stream and sandbox supervisor land in Phase 03.
package main

import (
	"net/http"
	"os"

	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/platform/buildinfo"
	"github.com/agent-platform/agent-platform/internal/platform/config"
	"github.com/agent-platform/agent-platform/internal/platform/httpserver"
	"github.com/agent-platform/agent-platform/internal/platform/logging"
	"github.com/agent-platform/agent-platform/internal/platform/natsconn"
	"github.com/agent-platform/agent-platform/internal/platform/run"
)

type serviceConfig struct {
	config.Common

	WorkerID         string `env:"WORKER_ID"`
	HTTPAddr         string `env:"WORKER_HTTP_ADDR" envDefault:":8082"`
	NATSURL          string `env:"WORKER_NATS_URL" envDefault:"nats://localhost:4222"`
	OrchestratorAddr string `env:"WORKER_ORCHESTRATOR_GRPC_ADDR" envDefault:"localhost:9090"`
	Pool             string `env:"WORKER_POOL" envDefault:"default"`
	MaxConcurrency   int    `env:"WORKER_MAX_CONCURRENCY" envDefault:"4"`
}

func main() {
	cfg, err := config.Load[serviceConfig]()
	if err != nil {
		panic(err)
	}
	if cfg.WorkerID == "" {
		host, err := os.Hostname()
		if err != nil {
			panic(err)
		}
		cfg.WorkerID = "worker-" + host
	}

	log, err := logging.New(logging.Config{
		Service:     "worker",
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

	nc, err := natsconn.Connect(ctx, cfg.NATSURL, cfg.WorkerID)
	if err != nil {
		log.Fatal("nats unavailable", zap.Error(err))
	}
	defer nc.Close()

	srv := httpserver.New(httpserver.Options{
		Addr:   cfg.HTTPAddr,
		Logger: log,
		Checks: []httpserver.Check{natsconn.HealthCheck(nc)},
	})
	srv.Router().Get("/v1/version", func(w http.ResponseWriter, _ *http.Request) {
		httpserver.WriteJSON(w, http.StatusOK, buildinfo.Info())
	})

	log.Info("starting worker",
		zap.String("worker_id", cfg.WorkerID),
		zap.String("pool", cfg.Pool),
		zap.Int("max_concurrency", cfg.MaxConcurrency),
		zap.String("orchestrator", cfg.OrchestratorAddr),
	)

	if err := run.Group(ctx, srv.Start); err != nil {
		log.Fatal("worker stopped", zap.Error(err))
	}
	log.Info("worker stopped cleanly")
}
