// Package commands implements the API -> orchestrator gRPC contract.
//
// Phase 02 registers the service so the wire contract is exercised from day one; every handler is still
// Unimplemented. Phase 03 adds submit, cancel and drain as transactional operations.
package commands

import (
	orchestratorv1 "github.com/agent-platform/agent-platform/gen/orchestrator/v1"
	"go.uber.org/zap"
)

// Service is the ExecutionCommands implementation.
type Service struct {
	orchestratorv1.UnimplementedExecutionCommandsServer
	log *zap.Logger
}

// New builds the command service.
func New(log *zap.Logger) *Service {
	return &Service{log: log}
}
