// Command worker is the Go host that consumes durable task dispatches, holds leases and reports fenced results.
//
// The worker never writes PostgreSQL and never publishes execution state. It acknowledges a JetStream message only
// after the orchestrator has committed the result for that attempt.
package main

import (
	"net/http"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/messaging"
	"github.com/agent-platform/agent-platform/internal/platform/buildinfo"
	"github.com/agent-platform/agent-platform/internal/platform/config"
	"github.com/agent-platform/agent-platform/internal/platform/httpserver"
	"github.com/agent-platform/agent-platform/internal/platform/logging"
	"github.com/agent-platform/agent-platform/internal/platform/natsconn"
	"github.com/agent-platform/agent-platform/internal/platform/run"
	"github.com/agent-platform/agent-platform/internal/worker/control"
	"github.com/agent-platform/agent-platform/internal/worker/delivery"
)

type serviceConfig struct {
	config.Common

	WorkerID         string `env:"WORKER_ID"`
	HTTPAddr         string `env:"WORKER_HTTP_ADDR" envDefault:":8082"`
	NATSURL          string `env:"WORKER_NATS_URL" envDefault:"nats://localhost:4222"`
	OrchestratorAddr string `env:"WORKER_ORCHESTRATOR_GRPC_ADDR" envDefault:"localhost:9090"`
	OrchestratorTLS  bool   `env:"WORKER_ORCHESTRATOR_TLS" envDefault:"false"`
	Pool             string `env:"WORKER_POOL" envDefault:"default"`
	MaxConcurrency   int    `env:"WORKER_MAX_CONCURRENCY" envDefault:"4"`
	RuntimeABI       int32  `env:"WORKER_RUNTIME_ABI" envDefault:"1"`
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
		cfg.WorkerID = "wrk_" + sanitize(host)
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
	js, err := natsconn.JetStream(nc)
	if err != nil {
		log.Fatal("jetstream unavailable", zap.Error(err))
	}

	controlClient := control.New(control.Config{
		Address:        cfg.OrchestratorAddr,
		Insecure:       !cfg.OrchestratorTLS,
		WorkerID:       cfg.WorkerID,
		Pool:           cfg.Pool,
		MaxConcurrency: cfg.MaxConcurrency,
		BinaryVersion:  buildinfo.Version,
		RuntimeABIs:    []int32{cfg.RuntimeABI},
	}, log)

	consumerConfig := messaging.DefaultConsumerConfig(cfg.WorkerID, cfg.MaxConcurrency)
	handler := &delivery.Handler{
		Client:   controlClient,
		WorkerID: cfg.WorkerID,
		Execute:  delivery.Echo,
		Log:      log,
	}
	consumer, err := messaging.NewConsumer(ctx, js, consumerConfig, handler, log)
	if err != nil {
		log.Fatal("task consumer unavailable", zap.Error(err))
	}
	if err := consumer.Start(ctx); err != nil {
		log.Fatal("task consumer could not start", zap.Error(err))
	}
	defer consumer.Stop()

	srv := httpserver.New(httpserver.Options{
		Addr:   cfg.HTTPAddr,
		Logger: log,
		Checks: []httpserver.Check{natsconn.HealthCheck(nc), controlClient.Check},
	})
	srv.Router().Get("/v1/version", func(w http.ResponseWriter, _ *http.Request) {
		httpserver.WriteJSON(w, http.StatusOK, buildinfo.Info())
	})
	srv.Router().Get("/v1/consumer", func(w http.ResponseWriter, req *http.Request) {
		info, err := consumer.Info(req.Context())
		if err != nil {
			httpserver.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{
			"worker_id": cfg.WorkerID,
			"consumer":  consumer.Config().Durable,
			"stats":     consumer.Stats(),
			"broker": map[string]any{
				"num_pending":   info.NumPending,
				"num_ack_pend":  info.NumAckPending,
				"num_redeliver": info.NumRedelivered,
			},
		})
	})

	log.Info("starting worker",
		zap.String("worker_id", cfg.WorkerID),
		zap.String("pool", cfg.Pool),
		zap.Int("max_concurrency", cfg.MaxConcurrency),
		zap.String("orchestrator", cfg.OrchestratorAddr),
		zap.String("consumer", consumerConfig.Durable),
	)

	if err := run.Group(ctx, controlClient.Run, srv.Start); err != nil {
		log.Fatal("worker stopped", zap.Error(err))
	}
	log.Info("worker stopped cleanly")
}

func sanitize(value string) string {
	mapped := strings.Map(func(char rune) rune {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', char == '-', char == '_':
			return char
		default:
			return '-'
		}
	}, value)
	if len(mapped) > 60 {
		mapped = mapped[:60]
	}
	if len(mapped) < 8 {
		// The contract requires at least eight characters after the wrk_ prefix.
		mapped = "local-01"
	}
	return mapped
}
