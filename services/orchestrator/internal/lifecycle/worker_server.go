package lifecycle

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/agent-platform/agent-platform/gen/worker/v1"
)

// WorkerIDMetadataKey carries the worker identity on unary worker calls. Production binds identity to the mTLS
// certificate; development uses this header.
const WorkerIDMetadataKey = "x-worker-id"

// WorkerServer implements the worker <-> orchestrator control contract.
type WorkerServer struct {
	workerv1.UnimplementedWorkerControlServer
	service *Service
	log     *zap.Logger
}

// NewWorkerServer builds the control service.
func NewWorkerServer(service *Service, log *zap.Logger) *WorkerServer {
	return &WorkerServer{service: service, log: log}
}

// Connect holds one worker session: registration, heartbeats, acceptance and rejection.
func (s *WorkerServer) Connect(stream workerv1.WorkerControl_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "the first message must be hello")
	}
	registration, err := s.service.Register(stream.Context(), hello)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	workerID := registration.WorkerID
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.service.Disconnect(ctx, workerID, registration.SessionID); err != nil {
			s.log.Warn("worker disconnect update failed", zap.String("worker_id", workerID), zap.Error(err))
		}
	}()

	var sendMu sync.Mutex
	send := func(message *workerv1.OrchestratorMessage) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(message)
	}
	if err := send(&workerv1.OrchestratorMessage{
		Body: &workerv1.OrchestratorMessage_HelloAck{HelloAck: &workerv1.HelloAck{
			SessionId:           registration.SessionID,
			HeartbeatIntervalMs: 5000,
			LeaseTtlMs:          int32(LeaseTTL / time.Millisecond),
			ServerTime:          timestamppb.Now(),
		}},
	}); err != nil {
		return err
	}
	unregister := s.service.sessions.Register(workerID, send)
	defer unregister()

	for {
		message, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) || stream.Context().Err() != nil {
				return nil
			}
			return err
		}
		switch body := message.Body.(type) {
		case *workerv1.WorkerMessage_Hello:
			s.log.Warn("duplicate hello on worker session", zap.String("worker_id", workerID))
		case *workerv1.WorkerMessage_Heartbeat:
			heartbeat := body.Heartbeat
			renewals := make([]LeaseRenewal, 0, len(heartbeat.GetActiveAttempts()))
			for _, active := range heartbeat.GetActiveAttempts() {
				renewals = append(renewals, LeaseRenewal{
					TaskID:    active.GetTaskId(),
					AttemptID: active.GetAttemptId(),
					LeaseID:   active.GetLeaseId(),
				})
			}
			usage := WorkerUsage{
				RuntimeVersion: heartbeat.GetRuntimeVersion(),
				CPUPercent:     heartbeat.GetCpuUsagePercent(),
				MemoryUsedMB:   heartbeat.GetMemoryUsedMb(),
				Status:         workerStatusString(heartbeat.GetStatus()),
			}
			if err := s.service.Heartbeat(stream.Context(), workerID, heartbeat.GetCapacity(), renewals, usage); err != nil {
				s.log.Warn("worker heartbeat failed", zap.String("worker_id", workerID), zap.Error(err))
			}
		case *workerv1.WorkerMessage_TaskAccepted:
			accepted := body.TaskAccepted
			if err := s.service.AcceptTask(stream.Context(), workerID,
				accepted.GetTaskId(), accepted.GetAttemptId(), accepted.GetLeaseId()); err != nil {
				s.log.Warn("task acceptance rejected",
					zap.String("worker_id", workerID),
					zap.String("task_id", accepted.GetTaskId()),
					zap.String("attempt_id", accepted.GetAttemptId()),
					zap.Error(err),
				)
			}
		case *workerv1.WorkerMessage_TaskRejected:
			rejected := body.TaskRejected
			if err := s.service.RejectTask(stream.Context(), workerID,
				rejected.GetTaskId(), rejected.GetAttemptId(), rejected.GetLeaseId(),
				rejectionCode(rejected.GetReason()), rejected.GetMessage()); err != nil {
				s.log.Warn("task rejection failed",
					zap.String("worker_id", workerID),
					zap.String("task_id", rejected.GetTaskId()),
					zap.Error(err),
				)
			}
		case *workerv1.WorkerMessage_Goodbye:
			s.log.Info("worker said goodbye", zap.String("worker_id", workerID), zap.String("reason", body.Goodbye.GetReason()))
			return nil
		case *workerv1.WorkerMessage_TaskProgress:
			// Advisory telemetry only; never a state transition.
		default:
			s.log.Debug("unknown worker message ignored", zap.String("worker_id", workerID))
		}
	}
}

// ReportTaskResult commits one fenced result and returns whether the worker may acknowledge the broker message.
func (s *WorkerServer) ReportTaskResult(ctx context.Context,
	req *workerv1.ReportTaskResultRequest) (*workerv1.ReportTaskResultResponse, error) {
	workerID := workerIDFromContext(ctx)
	if workerID == "" {
		return nil, status.Error(codes.Unauthenticated, "worker identity is required")
	}
	if active, err := s.service.IsActiveWorker(ctx, workerID); err != nil {
		return nil, status.Error(codes.Internal, "worker lookup failed")
	} else if !active {
		return nil, status.Error(codes.PermissionDenied, "worker is not registered or is offline")
	}
	response, err := s.service.ReportTaskResult(ctx, workerID, req)
	if err != nil {
		s.log.Error("result commit failed",
			zap.String("worker_id", workerID),
			zap.String("task_id", req.GetTaskId()),
			zap.String("attempt_id", req.GetAttemptId()),
			zap.Error(err),
		)
		return nil, status.Error(codes.Internal, "result could not be committed")
	}
	return &workerv1.ReportTaskResultResponse{
		Status:           resultStatus(response.Status),
		CurrentTaskState: response.TaskState,
		Message:          response.Message,
	}, nil
}

// GetTaskContext returns the pinned, immutable context for a live attempt. The worker fetches it after
// acceptance so the bus stays small and no secrets or blobs travel on it.
func (s *WorkerServer) GetTaskContext(ctx context.Context,
	req *workerv1.GetTaskContextRequest) (*workerv1.GetTaskContextResponse, error) {
	workerID := workerIDFromContext(ctx)
	if workerID == "" {
		return nil, status.Error(codes.Unauthenticated, "worker identity is required")
	}
	if req.GetTaskId() == "" || req.GetAttemptId() == "" || req.GetLeaseId() == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id, attempt_id and lease_id are required")
	}
	task, err := s.service.GetTaskContext(ctx, workerID, req.GetTaskId(), req.GetAttemptId(), req.GetLeaseId())
	if err != nil {
		if errors.Is(err, ErrStaleLease) {
			return nil, status.Error(codes.FailedPrecondition, "attempt is not owned by this worker")
		}
		s.log.Error("task context load failed",
			zap.String("worker_id", workerID), zap.String("task_id", req.GetTaskId()), zap.Error(err))
		return nil, status.Error(codes.Internal, "task context could not be loaded")
	}

	response := &workerv1.GetTaskContextResponse{
		OrgId:          task.OrgID,
		ProjectId:      task.ProjectID,
		ExecutionId:    task.ExecutionID,
		NodeId:         task.NodeID,
		NodeType:       task.NodeType,
		AgentVersionId: task.AgentVersionID,
		ToolVersionId:  task.ToolVersionID,
		Deadline:       timestamppb.New(task.Deadline),
		Traceparent:    s.service.TraceparentFor(ctx, task.ExecutionID),
		IdempotencyKey: task.IdempotencyKey,
	}
	if task.ResumeCheckpoint != nil {
		response.ResumeCheckpointBytes = task.ResumeCheckpoint.Bytes
		response.ResumeCheckpointId = task.ResumeCheckpoint.ID
		response.ResumeCheckpointSchemaVersion = int32(task.ResumeCheckpoint.SchemaVersion) //nolint:gosec // bounded by schema validation.
		response.ResumeCheckpointSequence = int32(task.ResumeCheckpoint.Sequence)           //nolint:gosec // bounded by the runtime.
	}
	response.VersionSpec, _ = structpb.NewStruct(task.VersionSpec)
	response.ExecutionInput, _ = structpb.NewStruct(task.ExecutionInput)
	response.NodeParams, _ = structpb.NewStruct(task.NodeParams)
	if task.OutputSchema != nil {
		response.NodeOutputSchema, _ = structpb.NewStruct(task.OutputSchema)
	}
	if len(task.UpstreamOutputs) > 0 {
		response.UpstreamOutputs = make(map[string]*structpb.Struct, len(task.UpstreamOutputs))
		for nodeID, output := range task.UpstreamOutputs {
			upstream, err := structpb.NewStruct(output)
			if err != nil {
				continue
			}
			response.UpstreamOutputs[nodeID] = upstream
		}
	}
	return response, nil
}

// GetTaskCredentials is the credentials seam. No secret store is wired yet, so it returns an empty set for any
// attempt that passes the fence; the runtime treats a missing credential ref as a configuration failure.
func (s *WorkerServer) GetTaskCredentials(ctx context.Context,
	req *workerv1.GetTaskCredentialsRequest) (*workerv1.GetTaskCredentialsResponse, error) {
	workerID := workerIDFromContext(ctx)
	if workerID == "" {
		return nil, status.Error(codes.Unauthenticated, "worker identity is required")
	}
	if _, err := s.service.GetTaskContext(ctx, workerID, req.GetTaskId(), req.GetAttemptId(), req.GetLeaseId()); err != nil {
		if errors.Is(err, ErrStaleLease) {
			return nil, status.Error(codes.FailedPrecondition, "attempt is not owned by this worker")
		}
		return nil, status.Error(codes.Internal, "credentials could not be authorised")
	}
	return &workerv1.GetTaskCredentialsResponse{
		Values:    map[string]string{},
		ExpiresAt: timestamppb.New(time.Now().UTC().Add(15 * time.Minute)),
	}, nil
}

// PutCheckpoint stores one checkpoint uploaded by an in-flight attempt.
func (s *WorkerServer) PutCheckpoint(ctx context.Context,
	req *workerv1.PutCheckpointRequest) (*workerv1.PutCheckpointResponse, error) {
	workerID := workerIDFromContext(ctx)
	if workerID == "" {
		return nil, status.Error(codes.Unauthenticated, "worker identity is required")
	}
	if active, err := s.service.IsActiveWorker(ctx, workerID); err != nil {
		return nil, status.Error(codes.Internal, "worker lookup failed")
	} else if !active {
		return nil, status.Error(codes.PermissionDenied, "worker is not registered or is offline")
	}
	result, err := s.service.PutCheckpoint(ctx, workerID, req)
	if err != nil {
		var checkpointErr *CheckpointError
		if errors.As(err, &checkpointErr) {
			code := codes.InvalidArgument
			switch checkpointErr.Code {
			case "CHECKPOINT_STALE_LEASE":
				code = codes.FailedPrecondition
			case "CHECKPOINT_STORE_FAILED":
				code = codes.Unavailable
			}
			return nil, status.Error(code, checkpointErr.Message)
		}
		s.log.Error("checkpoint commit failed",
			zap.String("worker_id", workerID),
			zap.String("task_id", req.GetTaskId()),
			zap.String("attempt_id", req.GetAttemptId()),
			zap.Error(err),
		)
		return nil, status.Error(codes.Internal, "checkpoint could not be committed")
	}
	return &workerv1.PutCheckpointResponse{
		CheckpointId: result.ID,
		Duplicate:    result.Duplicate,
		SizeBytes:    result.SizeBytes,
	}, nil
}

// IsActiveWorker reports whether the worker is registered and currently active.
func (s *Service) IsActiveWorker(ctx context.Context, workerID string) (bool, error) {
	var active bool
	err := s.pool.QueryRow(ctx, `
		SELECT status <> 'terminated' AND last_heartbeat_at > now() - make_interval(secs => $2)
		FROM execution.workers WHERE id = $1`, workerID, int(s.workerTerminateAfter/time.Second)).Scan(&active)
	if isNoRows(err) {
		return false, nil
	}
	return active, err
}

func workerStatusString(status workerv1.WorkerStatus) string {
	switch status {
	case workerv1.WorkerStatus_WORKER_STATUS_STARTING:
		return "starting"
	case workerv1.WorkerStatus_WORKER_STATUS_BUSY:
		return "busy"
	case workerv1.WorkerStatus_WORKER_STATUS_DRAINING:
		return "draining"
	case workerv1.WorkerStatus_WORKER_STATUS_UNHEALTHY:
		return "unhealthy"
	case workerv1.WorkerStatus_WORKER_STATUS_TERMINATED:
		return "terminated"
	default:
		return "available"
	}
}

func workerIDFromContext(ctx context.Context) string {
	values := metadata.ValueFromIncomingContext(ctx, WorkerIDMetadataKey)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func resultStatus(value ResultStatus) workerv1.ResultStatus {
	switch value {
	case ResultAccepted:
		return workerv1.ResultStatus_RESULT_STATUS_ACCEPTED
	case ResultAlreadyFinished:
		return workerv1.ResultStatus_RESULT_STATUS_ALREADY_FINISHED
	case ResultStaleLease:
		return workerv1.ResultStatus_RESULT_STATUS_STALE_LEASE
	case ResultValidationFailed:
		return workerv1.ResultStatus_RESULT_STATUS_VALIDATION_FAILED
	default:
		return workerv1.ResultStatus_RESULT_STATUS_RETRY_LATER
	}
}

func rejectionCode(reason workerv1.TaskRejectionReason) string {
	switch reason {
	case workerv1.TaskRejectionReason_TASK_REJECTION_REASON_BUSY:
		return "WORKER_BUSY"
	case workerv1.TaskRejectionReason_TASK_REJECTION_REASON_WRONG_WORKER:
		return "WRONG_WORKER"
	case workerv1.TaskRejectionReason_TASK_REJECTION_REASON_INVALID_PAYLOAD:
		return "INVALID_PAYLOAD"
	case workerv1.TaskRejectionReason_TASK_REJECTION_REASON_UNSUPPORTED_RUNTIME:
		return "UNSUPPORTED_RUNTIME"
	case workerv1.TaskRejectionReason_TASK_REJECTION_REASON_SHUTTING_DOWN:
		return "WORKER_SHUTTING_DOWN"
	case workerv1.TaskRejectionReason_TASK_REJECTION_REASON_ATTEMPT_NOT_OPEN:
		return "ATTEMPT_NOT_OPEN"
	default:
		return "WORKER_REJECTED"
	}
}
