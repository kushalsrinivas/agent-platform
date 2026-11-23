// Package commands implements the API -> orchestrator gRPC contract.
package commands

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	orchestratorv1 "github.com/agent-platform/agent-platform/gen/orchestrator/v1"
)

// Service is the ExecutionCommands implementation.
type Service struct {
	orchestratorv1.UnimplementedExecutionCommandsServer
	pool   *pgxpool.Pool
	log    *zap.Logger
	quotas Quotas
}

// New builds the command service. Quotas are per project.
func New(log *zap.Logger, pool *pgxpool.Pool, quotas Quotas) *Service {
	return &Service{log: log, pool: pool, quotas: quotas}
}
