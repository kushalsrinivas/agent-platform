// Command worker is the Go host that consumes durable task dispatches, holds leases and reports fenced results.
//
// The worker never writes PostgreSQL and never publishes execution state. It acknowledges a JetStream message only
// after the orchestrator has committed the result for that attempt. On shutdown it drains: no new work is accepted,
// in-flight attempts finish within the grace period, leases are left to expire if they do not, and the session is
// reported terminated.
package main

import (
	"context"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

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
	"github.com/agent-platform/agent-platform/internal/worker/execution"
)

type serviceConfig struct {
	config.Common

	WorkerID          string  `env:"WORKER_ID"`
	HTTPAddr          string  `env:"WORKER_HTTP_ADDR" envDefault:":8082"`
	NATSURL           string  `env:"WORKER_NATS_URL" envDefault:"nats://localhost:4222"`
	OrchestratorAddr  string  `env:"WORKER_ORCHESTRATOR_GRPC_ADDR" envDefault:"localhost:9090"`
	OrchestratorTLS   bool    `env:"WORKER_ORCHESTRATOR_TLS" envDefault:"false"`
	Pool              string  `env:"WORKER_POOL" envDefault:"default"`
	Runtime           string  `env:"WORKER_RUNTIME" envDefault:"python"`
	RuntimeVersion    string  `env:"WORKER_RUNTIME_VERSION" envDefault:"0.1.0"`
	Capabilities      string  `env:"WORKER_CAPABILITIES" envDefault:"agent_execution,tool_execution"`
	CPUCores          float64 `env:"WORKER_CPU_CORES" envDefault:"0"`
	MemoryMB          int64   `env:"WORKER_MEMORY_MB" envDefault:"2048"`
	MaxConcurrency    int     `env:"WORKER_MAX_CONCURRENCY" envDefault:"4"`
	RuntimeABI        int32   `env:"WORKER_RUNTIME_ABI" envDefault:"1"`
	RuntimeCommand    string  `env:"WORKER_RUNTIME_COMMAND"`
	RuntimeStartupSec int     `env:"WORKER_RUNTIME_STARTUP_SECONDS" envDefault:"15"`
	RuntimeKillSec    int     `env:"WORKER_RUNTIME_KILL_SECONDS" envDefault:"5"`
	HeartbeatMillis   int     `env:"WORKER_HEARTBEAT_MS" envDefault:"5000"`
	ShutdownGraceSec  int     `env:"WORKER_SHUTDOWN_GRACE_SECONDS" envDefault:"25"`
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
	if cfg.CPUCores <= 0 {
		cfg.CPUCores = float64(runtime.NumCPU())
	}
	if cfg.ShutdownGraceSec < 1 {
		cfg.ShutdownGraceSec = 1
	}
	capabilities := parseCapabilities(cfg.Capabilities)

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

	signalCtx := run.SignalContext()

	nc, err := natsconn.Connect(signalCtx, cfg.NATSURL, cfg.WorkerID)
	if err != nil {
		log.Fatal("nats unavailable", zap.Error(err))
	}
	defer nc.Close()
	js, err := natsconn.JetStream(nc)
	if err != nil {
		log.Fatal("jetstream unavailable", zap.Error(err))
	}

	controlClient := control.New(control.Config{
		Address:         cfg.OrchestratorAddr,
		Insecure:        !cfg.OrchestratorTLS,
		WorkerID:        cfg.WorkerID,
		Pool:            cfg.Pool,
		Runtime:         cfg.Runtime,
		RuntimeVersion:  cfg.RuntimeVersion,
		Capabilities:    capabilities,
		CPUCores:        cfg.CPUCores,
		MemoryMB:        cfg.MemoryMB,
		MaxConcurrency:  cfg.MaxConcurrency,
		BinaryVersion:   buildinfo.Version,
		RuntimeABIs:     []int32{cfg.RuntimeABI},
		HeartbeatPeriod: time.Duration(cfg.HeartbeatMillis) * time.Millisecond,
	}, log)

	consumerConfig := messaging.DefaultConsumerConfig(cfg.WorkerID, cfg.MaxConcurrency)
	var executor delivery.Executor = delivery.Echo
	if cfg.RuntimeCommand != "" {
		runtimeExecutor := execution.NewExecutor(controlClient, execution.Config{
			CommandTemplate: cfg.RuntimeCommand,
			RuntimeABI:      cfg.RuntimeABI,
			RuntimeVersion:  cfg.RuntimeVersion,
			StartupTimeout:  time.Duration(cfg.RuntimeStartupSec) * time.Second,
			KillGrace:       time.Duration(cfg.RuntimeKillSec) * time.Second,
		}, log)
		executor = runtimeExecutor.Execute
		log.Info("python runtime executor enabled",
			zap.String("command", cfg.RuntimeCommand),
			zap.Int32("runtime_abi", cfg.RuntimeABI),
			zap.Duration("startup_timeout", time.Duration(cfg.RuntimeStartupSec)*time.Second),
		)
	} else {
		log.Warn("no runtime command configured; using the offline echo executor")
	}
	handler := &delivery.Handler{
		Client:   controlClient,
		WorkerID: cfg.WorkerID,
		Execute:  executor,
		Log:      log,
	}
	consumer, err := messaging.NewConsumer(signalCtx, js, consumerConfig, handler, log)
	if err != nil {
		log.Fatal("task consumer unavailable", zap.Error(err))
	}

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
			"worker_id":    cfg.WorkerID,
			"runtime":      cfg.Runtime,
			"runtime_ver":  cfg.RuntimeVersion,
			"capabilities": capabilities,
			"draining":     controlClient.Draining(),
			"active_count": controlClient.ActiveCount(),
			"consumer":     consumer.Config().Durable,
			"stats":        consumer.Stats(),
			"broker": map[string]any{
				"num_pending":   info.NumPending,
				"num_ack_pend":  info.NumAckPending,
				"num_redeliver": info.NumRedelivered,
			},
		})
	})

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		if err := controlClient.Run(runCtx); err != nil {
			log.Error("worker control loop stopped", zap.Error(err))
		}
	}()
	go func() {
		defer group.Done()
		if err := srv.Start(runCtx); err != nil {
			log.Error("worker HTTP server stopped", zap.Error(err))
		}
	}()
	if err := consumer.Start(runCtx); err != nil {
		log.Fatal("task consumer could not start", zap.Error(err))
	}

	log.Info("starting worker",
		zap.String("worker_id", cfg.WorkerID),
		zap.String("pool", cfg.Pool),
		zap.String("runtime", cfg.Runtime),
		zap.String("runtime_version", cfg.RuntimeVersion),
		zap.Bool("python_executor", cfg.RuntimeCommand != ""),
		zap.Strings("capabilities", capabilities),
		zap.Float64("cpu_cores", cfg.CPUCores),
		zap.Int64("memory_mb", cfg.MemoryMB),
		zap.Int("max_concurrency", cfg.MaxConcurrency),
		zap.String("orchestrator", cfg.OrchestratorAddr),
		zap.String("consumer", consumerConfig.Durable),
	)

	select {
	case <-signalCtx.Done():
		log.Info("worker shutdown requested")
	case <-controlClient.DrainCh():
		log.Info("worker drain requested by the control plane")
	}

	// Stop accepting new dispatches, then let in-flight attempts report their fenced results.
	controlClient.BeginDrain()
	log.Info("worker draining", zap.Int("active_attempts", controlClient.ActiveCount()))
	consumer.Stop()
	grace := time.Duration(cfg.ShutdownGraceSec) * time.Second
	deadline := time.Now().Add(grace)
	for controlClient.ActiveCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if active := controlClient.ActiveCount(); active > 0 {
		log.Warn("shutdown grace expired with active attempts; leases will expire",
			zap.Int("active_attempts", active), zap.Duration("grace", grace))
	}
	controlClient.Shutdown()
	cancelRun()
	group.Wait()
	log.Info("worker stopped cleanly")
}

func parseCapabilities(value string) []string {
	result := make([]string, 0, 4)
	seen := map[string]struct{}{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" || len(item) > 64 {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	if len(result) == 0 {
		return []string{"agent_execution", "tool_execution"}
	}
	return result
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
