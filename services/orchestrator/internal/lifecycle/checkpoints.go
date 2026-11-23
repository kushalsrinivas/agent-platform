// Package lifecycle checkpoint storage. Checkpoints are the durable state of an execution node: the runtime
// writes them at safe boundaries, the worker uploads them through a fenced RPC, and the orchestrator records the
// lineage. Small states stay inline; large states are offloaded to the artifact store so PostgreSQL stays lean.
package lifecycle

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	workerv1 "github.com/agent-platform/agent-platform/gen/worker/v1"
	"github.com/agent-platform/agent-platform/internal/platform/idgen"
)

const (
	// CheckpointSchemaVersion is the only Python checkpoint envelope this orchestrator accepts.
	CheckpointSchemaVersion = 1
	// MaxCheckpointBytes matches the runtime ABI and the database constraint.
	MaxCheckpointBytes = 32 << 20
	// checkpointInlineThreshold keeps typical agent states in PostgreSQL and offloads the rest.
	checkpointInlineThreshold = 256 << 10
	// maxCheckpointJSON protects against a compressed payload that expands without bound.
	maxCheckpointJSON = 64 << 20
)

// CheckpointResult is the outcome of one fenced upload.
type CheckpointResult struct {
	ID         string
	Duplicate  bool
	SizeBytes  int64
	ParentID   string
	Sequence   int
	Schema     int
	ObjectKey  string
	Inline     bool
	RuntimeABI int
}

// CheckpointError is a client-visible checkpoint rejection.
type CheckpointError struct {
	Code    string
	Message string
}

func (e *CheckpointError) Error() string { return e.Message }

// CheckpointErrorCode extracts a stable rejection code.
func CheckpointErrorCode(err error) string {
	var checkpointErr *CheckpointError
	if errors.As(err, &checkpointErr) {
		return checkpointErr.Code
	}
	return ""
}

// PutCheckpoint validates and stores one checkpoint produced by an in-flight attempt.
func (s *Service) PutCheckpoint(ctx context.Context, workerID string,
	req *workerv1.PutCheckpointRequest) (CheckpointResult, error) {
	input, err := s.validateCheckpoint(workerID, req)
	if err != nil {
		return CheckpointResult{}, err
	}

	result := CheckpointResult{}
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		e, err := s.lockExecutionForTask(ctx, tx, input.taskID)
		if err != nil {
			if isNoRows(err) {
				return &CheckpointError{Code: "CHECKPOINT_STALE_LEASE", Message: "unknown task"}
			}
			return err
		}
		t, err := s.lockTask(ctx, tx, input.taskID)
		if err != nil {
			if isNoRows(err) {
				return &CheckpointError{Code: "CHECKPOINT_STALE_LEASE", Message: "unknown task"}
			}
			return err
		}
		a, err := s.lockAttempt(ctx, tx, input.attemptID)
		if err != nil {
			if isNoRows(err) {
				return &CheckpointError{Code: "CHECKPOINT_STALE_LEASE", Message: "unknown attempt"}
			}
			return err
		}
		if a.TaskID != t.ID || a.ExecutionID != e.ID || a.WorkerID != input.workerID ||
			a.LeaseID != input.leaseID || a.Outcome != nil ||
			t.OwnerWorkerID == nil || *t.OwnerWorkerID != input.workerID ||
			t.LeaseID == nil || *t.LeaseID != input.leaseID || t.LeaseExpired ||
			(t.State != "assigned" && t.State != "running") {
			return &CheckpointError{Code: "CHECKPOINT_STALE_LEASE", Message: "attempt is not owned by this worker"}
		}
		if t.NodeType != "agent" || t.AgentVersionID == nil || *t.AgentVersionID == "" {
			return &CheckpointError{Code: "CHECKPOINT_UNSUPPORTED", Message: "checkpoints are only supported for agent nodes"}
		}
		if t.RuntimeABI != nil && *t.RuntimeABI != input.runtimeABI {
			return &CheckpointError{
				Code:    "CHECKPOINT_UNSUPPORTED",
				Message: fmt.Sprintf("runtime ABI %d does not match the pinned ABI %d", input.runtimeABI, *t.RuntimeABI),
			}
		}

		var existingID, existingSHA string
		err = tx.QueryRow(ctx, `
			SELECT id, sha256 FROM execution.checkpoints
			WHERE attempt_id = $1 AND sequence = $2`, a.ID, input.sequence).
			Scan(&existingID, &existingSHA)
		if err == nil {
			if existingSHA != input.sha256 {
				return &CheckpointError{
					Code:    "CHECKPOINT_CONFLICT",
					Message: "a different checkpoint already exists for this attempt sequence",
				}
			}
			result = CheckpointResult{ID: existingID, Duplicate: true, SizeBytes: input.size,
				Sequence: input.sequence, Schema: input.schemaVersion}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		// Sequence is monotonic within one attempt; lineage across retries follows created_at and the parent link.
		var parentID pgtype.Text
		if err := tx.QueryRow(ctx, `
			SELECT id FROM execution.checkpoints
			WHERE task_id = $1
			ORDER BY created_at DESC, sequence DESC
			LIMIT 1`, t.ID).Scan(&parentID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		checkpointID := idgen.New("ckpt")
		var objectKey *string
		var inline []byte
		useInline := len(input.state) <= checkpointInlineThreshold || s.artifacts == nil
		if useInline {
			inline = input.state
		} else {
			key := fmt.Sprintf("checkpoints/%s/%s.json.gz", e.ID, checkpointID)
			if err := s.artifacts.Put(ctx, key, input.state); err != nil {
				return &CheckpointError{Code: "CHECKPOINT_STORE_FAILED", Message: "could not store the checkpoint payload"}
			}
			objectKey = &key
		}
		var parent *string
		if parentID.Valid {
			value := parentID.String
			parent = &value
		}
		var idempotencyKey *string
		if t.IdempotencyKey != nil {
			value := *t.IdempotencyKey
			idempotencyKey = &value
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO execution.checkpoints
				(id, execution_id, task_id, attempt_id, org_id, object_key, state_inline, sha256, size_bytes,
				 checkpoint_schema_version, runtime_abi, runtime_version, agent_version_id, node_id,
				 workflow_version_id, parent_checkpoint_id, sequence, idempotency_key)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
			checkpointID, e.ID, t.ID, a.ID, e.OrgID, objectKey, inline, input.sha256, input.size,
			input.schemaVersion, input.runtimeABI, input.runtimeVersion, *t.AgentVersionID, t.NodeID,
			e.WorkflowVersionID, parent, input.sequence, idempotencyKey); err != nil {
			return fmt.Errorf("lifecycle: insert checkpoint: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE execution.task_attempts SET checkpoint_id = $2 WHERE id = $1`, a.ID, checkpointID); err != nil {
			return err
		}

		result = CheckpointResult{
			ID: checkpointID, SizeBytes: input.size, Sequence: input.sequence,
			Schema: input.schemaVersion, Inline: useInline, RuntimeABI: input.runtimeABI,
		}
		if objectKey != nil {
			result.ObjectKey = *objectKey
		}
		if parent != nil {
			result.ParentID = *parent
		}
		s.log.Debug("checkpoint committed",
			zap.String("checkpoint_id", checkpointID),
			zap.String("task_id", t.ID),
			zap.String("attempt_id", a.ID),
			zap.Int("sequence", input.sequence),
			zap.Int("size_bytes", len(input.state)),
			zap.Bool("inline", useInline),
		)
		return nil
	})
	if err != nil {
		return CheckpointResult{}, err
	}
	return result, nil
}

type checkpointInput struct {
	workerID       string
	taskID         string
	attemptID      string
	leaseID        string
	sequence       int
	schemaVersion  int
	runtimeABI     int
	runtimeVersion string
	sha256         string
	size           int64
	state          []byte
}

func (s *Service) validateCheckpoint(workerID string, req *workerv1.PutCheckpointRequest) (*checkpointInput, error) {
	if req == nil {
		return nil, &CheckpointError{Code: "CHECKPOINT_INVALID", Message: "checkpoint is required"}
	}
	if workerID == "" || req.GetTaskId() == "" || req.GetAttemptId() == "" || req.GetLeaseId() == "" {
		return nil, &CheckpointError{Code: "CHECKPOINT_INVALID", Message: "worker, task, attempt and lease are required"}
	}
	if req.GetSequence() < 1 {
		return nil, &CheckpointError{Code: "CHECKPOINT_INVALID", Message: "sequence must be at least 1"}
	}
	if req.GetCheckpointSchemaVersion() != CheckpointSchemaVersion {
		return nil, &CheckpointError{
			Code:    "CHECKPOINT_UNSUPPORTED",
			Message: fmt.Sprintf("checkpoint schema %d is not supported", req.GetCheckpointSchemaVersion()),
		}
	}
	if req.GetRuntimeAbi() < 1 {
		return nil, &CheckpointError{Code: "CHECKPOINT_INVALID", Message: "runtime_abi must be at least 1"}
	}
	if req.GetRuntimeVersion() == "" || len(req.GetRuntimeVersion()) > 128 {
		return nil, &CheckpointError{Code: "CHECKPOINT_INVALID", Message: "runtime_version is required"}
	}
	if !strings.HasPrefix(req.GetSha256(), "sha256:") {
		return nil, &CheckpointError{Code: "CHECKPOINT_INVALID", Message: "sha256 must be formatted as sha256:<hex>"}
	}
	state := req.GetState()
	if len(state) == 0 || len(state) > MaxCheckpointBytes {
		return nil, &CheckpointError{
			Code:    "CHECKPOINT_INVALID",
			Message: fmt.Sprintf("checkpoint must be between 1 and %d bytes", MaxCheckpointBytes),
		}
	}
	digest := sha256.Sum256(state)
	expected := "sha256:" + hex.EncodeToString(digest[:])
	if !strings.EqualFold(expected, req.GetSha256()) {
		return nil, &CheckpointError{Code: "CHECKPOINT_CHECKSUM_MISMATCH", Message: "checkpoint checksum does not match its bytes"}
	}
	if err := validateCheckpointPayload(state); err != nil {
		return nil, err
	}
	return &checkpointInput{
		workerID: workerID, taskID: req.GetTaskId(), attemptID: req.GetAttemptId(),
		leaseID: req.GetLeaseId(), sequence: int(req.GetSequence()),
		schemaVersion: int(req.GetCheckpointSchemaVersion()), runtimeABI: int(req.GetRuntimeAbi()),
		runtimeVersion: req.GetRuntimeVersion(), sha256: expected, size: int64(len(state)), state: state,
	}, nil
}

// validateCheckpointPayload rejects unsafe or corrupt state before it becomes durable. Only gzip-compressed JSON
// is accepted: pickle or arbitrary binary can never be deserialised by a future runtime.
func validateCheckpointPayload(data []byte) error {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return &CheckpointError{Code: "CHECKPOINT_INVALID", Message: "checkpoint is not gzip-compressed"}
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return &CheckpointError{Code: "CHECKPOINT_INVALID", Message: "checkpoint gzip header is invalid"}
	}
	defer func() { _ = reader.Close() }()
	payload, err := io.ReadAll(io.LimitReader(reader, maxCheckpointJSON+1))
	if err != nil {
		return &CheckpointError{Code: "CHECKPOINT_INVALID", Message: "checkpoint payload could not be decompressed"}
	}
	if len(payload) > maxCheckpointJSON {
		return &CheckpointError{Code: "CHECKPOINT_INVALID", Message: "checkpoint expands beyond the supported state size"}
	}
	if !json.Valid(payload) {
		return &CheckpointError{Code: "CHECKPOINT_INVALID", Message: "checkpoint payload is not valid JSON"}
	}
	return nil
}

// ResumeCheckpoint is the latest durable state for a task.
type ResumeCheckpoint struct {
	ID            string
	Bytes         []byte
	SchemaVersion int
	Sequence      int
}

// latestCheckpoint returns the newest readable checkpoint for a task, excluding the current attempt. A corrupt or
// missing large artifact is skipped in favour of an older valid checkpoint instead of failing the attempt.
func (s *Service) latestCheckpoint(ctx context.Context, tx pgx.Tx, t *task, e *execution,
	currentAttemptID string) (*ResumeCheckpoint, error) {
	if t.NodeType != "agent" || t.AgentVersionID == nil {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id, COALESCE(object_key, ''), state_inline, sha256, checkpoint_schema_version, sequence
		FROM execution.checkpoints
		WHERE task_id = $1
		  AND agent_version_id = $2
		  AND workflow_version_id = $3
		  AND attempt_id <> $4
		  AND checkpoint_schema_version = $5
		ORDER BY created_at DESC, sequence DESC
		LIMIT 5`,
		t.ID, *t.AgentVersionID, e.WorkflowVersionID, currentAttemptID, CheckpointSchemaVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type candidate struct {
		id        string
		objectKey string
		inline    []byte
		sha256    string
		schema    int
		sequence  int
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.objectKey, &item.inline, &item.sha256, &item.schema, &item.sequence); err != nil {
			return nil, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, item := range candidates {
		data := item.inline
		if item.objectKey != "" {
			if s.artifacts == nil {
				continue
			}
			data, err = s.artifacts.Get(ctx, item.objectKey)
			if err != nil {
				s.log.Warn("checkpoint artifact unavailable; trying an older checkpoint",
					zap.String("checkpoint_id", item.id), zap.String("task_id", t.ID), zap.Error(err))
				continue
			}
		}
		if len(data) == 0 || len(data) > MaxCheckpointBytes {
			continue
		}
		digest := sha256.Sum256(data)
		if "sha256:"+hex.EncodeToString(digest[:]) != item.sha256 {
			s.log.Warn("checkpoint checksum mismatch; trying an older checkpoint",
				zap.String("checkpoint_id", item.id), zap.String("task_id", t.ID))
			continue
		}
		return &ResumeCheckpoint{
			ID: item.id, Bytes: data, SchemaVersion: item.schema, Sequence: item.sequence,
		}, nil
	}
	return nil, nil
}
