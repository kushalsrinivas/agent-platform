package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type config struct {
	APIURL               string `json:"api_url"`
	APIKey               string `json:"api_key,omitempty"`
	Token                string `json:"token,omitempty"`
	OrgID                string `json:"org_id,omitempty"`
	ProjectID            string `json:"project_id,omitempty"`
	Pool                 string `json:"pool,omitempty"`
	NATSURL              string `json:"nats_url,omitempty"`
	OrchestratorGRPCAddr string `json:"orchestrator_grpc_addr,omitempty"`
	RuntimeCommand       string `json:"runtime_command,omitempty"`
	WorkerBin            string `json:"worker_bin,omitempty"`
}

func defaultConfig() config {
	return config{
		APIURL:               "http://localhost:8080",
		Pool:                 "default",
		NATSURL:              "nats://localhost:4222",
		OrchestratorGRPCAddr: "localhost:9090",
		RuntimeCommand:       "python -m agent_runtime serve --socket {socket}",
		WorkerBin:            "bin/worker",
	}
}

func configPath() string {
	if path := strings.TrimSpace(os.Getenv("AGENTCTL_CONFIG")); path != "" {
		return path
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "agentctl", "config.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentctl", "config.json")
}

func loadConfig() (config, error) {
	cfg := defaultConfig()
	raw, err := os.ReadFile(configPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			applyEnv(&cfg)
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", configPath(), err)
	}
	applyEnv(&cfg)
	return cfg, nil
}

func saveConfig(cfg config) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func applyEnv(cfg *config) {
	if value := strings.TrimSpace(os.Getenv("AGENT_PLATFORM_URL")); value != "" {
		cfg.APIURL = value
	}
	if value := strings.TrimSpace(os.Getenv("AGENT_PLATFORM_API_KEY")); value != "" {
		cfg.APIKey = value
	}
	if value := strings.TrimSpace(os.Getenv("AGENT_PLATFORM_TOKEN")); value != "" {
		cfg.Token = value
	}
	if value := strings.TrimSpace(os.Getenv("AGENT_PLATFORM_ORG")); value != "" {
		cfg.OrgID = value
	}
	if value := strings.TrimSpace(os.Getenv("AGENT_PLATFORM_PROJECT")); value != "" {
		cfg.ProjectID = value
	}
	if value := strings.TrimSpace(os.Getenv("AGENTCTL_POOL")); value != "" {
		cfg.Pool = value
	}
	if value := strings.TrimSpace(os.Getenv("AGENTCTL_WORKER_BIN")); value != "" {
		cfg.WorkerBin = value
	}
	if value := strings.TrimSpace(os.Getenv("WORKER_NATS_URL")); value != "" {
		cfg.NATSURL = value
	}
	if value := strings.TrimSpace(os.Getenv("WORKER_ORCHESTRATOR_GRPC_ADDR")); value != "" {
		cfg.OrchestratorGRPCAddr = value
	}
	if value := os.Getenv("WORKER_RUNTIME_COMMAND"); strings.TrimSpace(value) != "" {
		cfg.RuntimeCommand = value
	}
}
