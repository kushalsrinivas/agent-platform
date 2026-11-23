package commands

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	orchestratorv1 "github.com/agent-platform/agent-platform/gen/orchestrator/v1"
	"github.com/agent-platform/agent-platform/services/orchestrator/internal/lifecycle"
)

// CancelExecution records a durable cancellation decision. The operation is idempotent while the execution is
// non-terminal; a terminal or failing execution returns accepted=false without changing state.
func (s *Service) CancelExecution(ctx context.Context,
	req *orchestratorv1.CancelExecutionRequest) (*orchestratorv1.CancelExecutionResponse, error) {
	if req.GetOrgId() == "" || req.GetExecutionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "org_id and execution_id are required")
	}
	if s.lifecycle == nil {
		return nil, status.Error(codes.FailedPrecondition, "cancellation is not configured")
	}
	outcome, err := s.lifecycle.CancelExecution(ctx, req.GetOrgId(), req.GetExecutionId(),
		req.GetRequestedBy(), req.GetReason())
	if err != nil {
		if errors.Is(err, lifecycle.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "execution not found")
		}
		s.log.Error("execution cancellation failed",
			zap.String("organization_id", req.GetOrgId()),
			zap.String("execution_id", req.GetExecutionId()),
			zap.Error(err),
		)
		return nil, status.Error(codes.Internal, "cancellation could not be committed")
	}
	return &orchestratorv1.CancelExecutionResponse{
		ExecutionId:  outcome.ExecutionID,
		State:        outcome.State,
		StateVersion: outcome.StateVersion,
		CancelEpoch:  int32(outcome.CancelEpoch), //nolint:gosec // bounded by the cancellation counter.
		Accepted:     outcome.Accepted,
	}, nil
}
