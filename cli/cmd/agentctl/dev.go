package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/agent-platform/agent-platform/sdk/go/client"
)

func devCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dev", flag.ExitOnError)
	file := fs.String("file", "workflow.json", "workflow JSON file")
	input := fs.String("input", "input.json", "input JSON or @file")
	pool := fs.String("pool", "", "worker pool (default from config)")
	workflowID := fs.String("workflow", "", "existing workflow id (otherwise create from --file)")
	version := fs.Int("version", 0, "workflow version (default latest)")
	noWorker := fs.Bool("no-worker", false, "use an already running worker")
	workerBin := fs.String("worker-bin", "", "worker binary path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if *pool == "" {
		*pool = cfg.Pool
	}
	api := newClient(cfg)

	payload, err := readJSONFile(*file)
	if err != nil {
		return err
	}
	graph, ok := payload["graph"].(map[string]any)
	if !ok {
		graph = payload
	}
	validation, err := api.ValidateWorkflow(ctx, cfg.ProjectID, graph)
	if err != nil {
		return err
	}
	fmt.Printf("workflow valid: %d nodes, %d edges\n", validation.Nodes, validation.Edges)

	var worker *exec.Cmd
	if !*noWorker {
		worker, err = startDevWorker(ctx, cfg, *pool, *workerBin)
		if err != nil {
			return err
		}
		defer stopDevWorker(worker)
		fmt.Printf("local worker started (pool=%s)\n", *pool)
	}

	if *workflowID == "" {
		name := firstNonEmpty(stringValue(payload["name"]), "dev")
		workflow, err := api.CreateWorkflow(ctx, cfg.ProjectID, name,
			stringValue(payload["description"]), graph)
		if err != nil {
			// A repeated dev run reuses the workflow name and appends a version when the graph changed.
			if apiErr, ok := err.(*client.APIError); ok && apiErr.StatusCode == http.StatusConflict {
				workflows, listErr := api.ListWorkflows(ctx, cfg.ProjectID, 100)
				if listErr != nil {
					return err
				}
				found := false
				for _, candidate := range workflows {
					if candidate.Name == name {
						workflow = candidate
						found = true
						break
					}
				}
				if !found {
					return err
				}
				if created, versionErr := api.CreateWorkflowVersion(ctx, cfg.ProjectID, workflow.ID, graph); versionErr == nil {
					workflow.LatestVersion = created.Version
				} else if apiErr, ok := versionErr.(*client.APIError); !ok || apiErr.StatusCode != http.StatusConflict {
					return versionErr
				}
				err = nil
			}
			if err != nil {
				return err
			}
		}
		deployed, err := api.DeployWorkflow(ctx, cfg.ProjectID, workflow.ID, workflow.LatestVersion)
		if err != nil {
			return err
		}
		*workflowID = workflow.ID
		*version = deployed.Version
		fmt.Printf("workflow %s v%d deployed\n", *workflowID, *version)
	} else if *version == 0 {
		workflow, err := api.GetWorkflow(ctx, cfg.ProjectID, *workflowID)
		if err != nil {
			return err
		}
		*version = workflow.LatestVersion
	}

	inputValue, err := readJSONValue(*input)
	if err != nil {
		return err
	}
	execution, err := api.ExecuteWorkflow(ctx, cfg.ProjectID, *workflowID, *version, inputValue,
		client.ExecuteOptions{})
	if err != nil {
		return err
	}
	fmt.Printf("execution %s %s\n", execution.ID, execution.State)
	if err := streamExecution(ctx, api, cfg.ProjectID, execution.ID, 0, true); err != nil {
		return err
	}
	final, err := api.WaitExecution(ctx, cfg.ProjectID, execution.ID, 10*time.Minute, 300*time.Millisecond)
	if err != nil {
		return err
	}
	fmt.Printf("final: %s\n", final.State)
	if final.State != "succeeded" {
		failure := ""
		if final.FailureCode != nil {
			failure = *final.FailureCode
		}
		return fmt.Errorf("local execution ended %s (failure=%s)", final.State, failure)
	}
	return nil
}

func startDevWorker(ctx context.Context, cfg config, pool, override string) (*exec.Cmd, error) {
	path := firstNonEmpty(override, cfg.WorkerBin, "bin/worker")
	if resolved, err := exec.LookPath(path); err == nil {
		path = resolved
	} else if !filepath.IsAbs(path) {
		if _, statErr := os.Stat(path); statErr != nil {
			return nil, errors.New("worker binary not found; run `make build-go`, pass --worker-bin, or use --no-worker")
		}
	}
	command := exec.CommandContext(ctx, path)
	env := append(os.Environ(),
		"WORKER_ID=wrk_dev_"+fmt.Sprint(time.Now().UnixNano()),
		"WORKER_POOL="+pool,
		"WORKER_NATS_URL="+cfg.NATSURL,
		"WORKER_ORCHESTRATOR_GRPC_ADDR="+cfg.OrchestratorGRPCAddr,
		"WORKER_ORCHESTRATOR_TLS=false",
		"WORKER_RUNTIME=python",
		"WORKER_RUNTIME_VERSION=0.1.0",
		"WORKER_CAPABILITIES=agent_execution,tool_execution",
		"WORKER_MAX_CONCURRENCY=2",
		"WORKER_HEARTBEAT_MS=1000",
		"WORKER_RUNTIME_COMMAND="+cfg.RuntimeCommand,
		"WORKER_RUNTIME_STARTUP_SECONDS=30",
	)
	command.Env = env
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start local worker: %w", err)
	}
	time.Sleep(2 * time.Second)
	return command, nil
}

func stopDevWorker(command *exec.Cmd) {
	if command == nil || command.Process == nil {
		return
	}
	_ = command.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
	}
}
