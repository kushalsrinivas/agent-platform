package commands

import (
	"context"
	"encoding/json"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	orchestratorv1 "github.com/agent-platform/agent-platform/gen/orchestrator/v1"
	"github.com/agent-platform/agent-platform/services/orchestrator/internal/lifecycle"
)

// SignalTask relays an approval or delay signal to the lifecycle engine.
func (s *Service) SignalTask(ctx context.Context,
	req *orchestratorv1.SignalTaskRequest) (*orchestratorv1.SignalTaskResponse, error) {
	if req.GetOrgId() == "" || req.GetProjectId() == "" || req.GetExecutionId() == "" || req.GetTaskId() == "" {
		return nil, status.Error(codes.InvalidArgument, "org, project, execution and task are required")
	}
	if s.lifecycle == nil {
		return nil, status.Error(codes.FailedPrecondition, "signals are not configured")
	}
	payload := map[string]any{}
	if req.GetPayload() != nil {
		payload = req.GetPayload().AsMap()
	}
	result, err := s.lifecycle.SignalTask(ctx, req.GetOrgId(), req.GetProjectId(), req.GetExecutionId(),
		req.GetTaskId(), req.GetAction(), req.GetComment(), req.GetActor(), payload)
	if err != nil {
		if errors.Is(err, lifecycle.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "execution or task not found")
		}
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	encoded, _ := json.Marshal(result.ControlState)
	return &orchestratorv1.SignalTaskResponse{
		TaskId:       result.TaskID,
		NodeType:     result.NodeType,
		ControlState: string(encoded),
		Accepted:     result.Accepted,
	}, nil
}
