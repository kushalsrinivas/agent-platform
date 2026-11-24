package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/structpb"

	workerv1 "github.com/agent-platform/agent-platform/gen/worker/v1"
	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/packages/contracts"
)

// DelegationPolicy is the frozen supervisor budget and allowlist.
type DelegationPolicy struct {
	AllowedAgents      []DelegationAgent `json:"allowed_agents"`
	MaxChildren        int               `json:"max_children"`
	MaxDepth           int               `json:"max_depth"`
	MaxParallel        int               `json:"max_parallel"`
	MaxTotalTokens     int64             `json:"max_total_tokens,omitempty"`
	MaxDurationSeconds int               `json:"max_duration_seconds,omitempty"`
	MaxRetries         int               `json:"max_retries"`
}

// DelegationAgent is one pinned child agent in a supervisor allowlist.
type DelegationAgent struct {
	AgentID string `json:"agent_id"`
	Version int    `json:"version"`
	Name    string `json:"name,omitempty"`
}

type delegationRecord struct {
	ID                string
	RootExecutionID   string
	ParentExecutionID string
	ParentTaskID      string
	ChildExecutionID  string
	AgentID           string
	AgentVersion      int
	Status            string
}

func parseDelegationPolicy(raw json.RawMessage) DelegationPolicy {
	policy := DelegationPolicy{MaxChildren: 3, MaxDepth: 3, MaxParallel: 3}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &policy)
	}
	if policy.MaxChildren < 1 {
		policy.MaxChildren = 3
	}
	if policy.MaxDepth < 1 {
		policy.MaxDepth = 3
	}
	if policy.MaxParallel < 1 {
		policy.MaxParallel = 3
	}
	if policy.MaxRetries < 0 {
		policy.MaxRetries = 0
	}
	return policy
}

func (p DelegationPolicy) allows(agentID string, version int) bool {
	for _, allowed := range p.AllowedAgents {
		if allowed.AgentID == agentID && allowed.Version == version {
			return true
		}
	}
	return false
}

// DelegateTask creates one delegated child execution for a supervisor attempt. The worker is only a transport;
// the allowlist, budgets and nesting limits are all enforced here.
func (s *Service) DelegateTask(ctx context.Context, workerID string,
	req *workerv1.DelegateTaskRequest) (*workerv1.DelegateTaskResponse, error) {
	if req == nil || req.GetTaskId() == "" || req.GetAttemptId() == "" || req.GetLeaseId() == "" {
		return nil, errors.New("parent task, attempt and lease are required")
	}
	var response *workerv1.DelegateTaskResponse
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		parent, parentTask, err := s.fencedParent(ctx, tx, workerID, req.GetTaskId(), req.GetAttemptId(), req.GetLeaseId())
		if err != nil {
			return err
		}
		policy, err := delegationPolicyForTask(ctx, tx, parentTask)
		if err != nil {
			return err
		}
		if len(policy.AllowedAgents) == 0 {
			return errors.New("agent is not configured as a supervisor")
		}
		if !policy.allows(req.GetAgentId(), int(req.GetAgentVersion())) {
			return fmt.Errorf("agent %s v%d is not in the supervisor allowlist", req.GetAgentId(), req.GetAgentVersion())
		}
		idempotencyKey := strings.TrimSpace(req.GetIdempotencyKey())
		if len(idempotencyKey) > 300 {
			return errors.New("delegation idempotency key is too long")
		}
		if idempotencyKey != "" {
			var existing delegationRecord
			err := tx.QueryRow(ctx, `
				SELECT id, root_execution_id, parent_execution_id, parent_task_id, child_execution_id,
				       agent_id, agent_version, status
				FROM execution.delegations
				WHERE parent_task_id = $1 AND idempotency_key = $2`, parentTask.ID, idempotencyKey).
				Scan(&existing.ID, &existing.RootExecutionID, &existing.ParentExecutionID, &existing.ParentTaskID,
					&existing.ChildExecutionID, &existing.AgentID, &existing.AgentVersion, &existing.Status)
			if err == nil {
				response = &workerv1.DelegateTaskResponse{
					DelegationId: existing.ID, ChildExecutionId: existing.ChildExecutionID, Status: existing.Status,
				}
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		rootID, depth, err := delegationRoot(ctx, tx, parent.ID)
		if err != nil {
			return err
		}
		if depth+1 > policy.MaxDepth {
			return fmt.Errorf("delegation depth %d exceeds the policy maximum %d", depth+1, policy.MaxDepth)
		}
		// Concurrent delegate calls from one supervisor must not each observe an under-budget tree.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, rootID); err != nil {
			return err
		}
		var children int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM execution.delegations WHERE root_execution_id = $1`, rootID).Scan(&children); err != nil {
			return err
		}
		if children >= policy.MaxChildren {
			return fmt.Errorf("delegation count %d reached the policy maximum %d", children, policy.MaxChildren)
		}
		var active int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM execution.delegations d
			JOIN execution.executions e ON e.id = d.child_execution_id
			WHERE d.root_execution_id = $1 AND e.state IN ('pending', 'running', 'failing', 'cancel_requested')`,
			rootID).Scan(&active); err != nil {
			return err
		}
		if active >= policy.MaxParallel {
			return fmt.Errorf("concurrent delegations %d reached the policy maximum %d", active, policy.MaxParallel)
		}
		if policy.MaxTotalTokens > 0 {
			var tokens int64
			if err := tx.QueryRow(ctx, `
				SELECT COALESCE(sum(
					COALESCE((a.metrics->>'tokens_in')::bigint, 0) +
					COALESCE((a.metrics->>'tokens_out')::bigint, 0)
				), 0)
				FROM execution.delegations d
				JOIN execution.task_attempts a ON a.execution_id = d.child_execution_id
				WHERE d.root_execution_id = $1`, rootID).Scan(&tokens); err != nil {
				return err
			}
			if tokens >= policy.MaxTotalTokens {
				return fmt.Errorf("delegated token budget %d reached the policy maximum %d", tokens, policy.MaxTotalTokens)
			}
		}

		delegationID := idgen.New("dlg")
		childID, err := s.createDelegatedChild(ctx, tx, parent, parentTask, policy, delegationID, req, depth+1)
		if err != nil {
			return err
		}
		contextRefs := req.GetContextRefs()
		if contextRefs == nil {
			contextRefs = []string{}
		}
		requestJSON, err := json.Marshal(map[string]any{
			"agent_id":      req.GetAgentId(),
			"agent_version": req.GetAgentVersion(),
			"task":          structMap(req.GetTask()),
			"context":       structMap(req.GetContext()),
			"context_refs":  contextRefs,
		})
		if err != nil {
			return err
		}
		contextJSON, _ := json.Marshal(structMap(req.GetContext()))
		if _, err := tx.Exec(ctx, `
			INSERT INTO execution.delegations
				(id, org_id, root_execution_id, parent_execution_id, parent_task_id, child_execution_id,
				 agent_id, agent_version, request, shared_context, context_refs, status, idempotency_key)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10::jsonb, $11, 'pending', $12)`,
			delegationID, parent.OrgID, rootID, parent.ID, parentTask.ID, childID,
			req.GetAgentId(), req.GetAgentVersion(), requestJSON, contextJSON, contextRefs,
			idempotencyKey); err != nil {
			return err
		}
		response = &workerv1.DelegateTaskResponse{
			DelegationId: delegationID, ChildExecutionId: childID, Status: "pending",
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func delegationPolicyForTask(ctx context.Context, tx pgx.Tx, t *task) (DelegationPolicy, error) {
	if t.AgentVersionID == nil {
		return DelegationPolicy{}, errors.New("only agent tasks may delegate")
	}
	var specJSON []byte
	if err := tx.QueryRow(ctx, `
		SELECT spec FROM control.agent_versions WHERE id = $1`, *t.AgentVersionID).Scan(&specJSON); err != nil {
		return DelegationPolicy{}, err
	}
	var spec struct {
		Orchestration json.RawMessage `json:"orchestration"`
	}
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return DelegationPolicy{}, errors.New("supervisor agent spec is invalid")
	}
	policy := parseDelegationPolicy(spec.Orchestration)
	return policy, nil
}

func (s *Service) fencedParent(ctx context.Context, tx pgx.Tx, workerID, taskID, attemptID, leaseID string) (*execution, *task, error) {
	e, err := s.lockExecutionForTask(ctx, tx, taskID)
	if err != nil {
		return nil, nil, err
	}
	t, err := s.lockTask(ctx, tx, taskID)
	if err != nil {
		return nil, nil, err
	}
	a, err := s.lockAttempt(ctx, tx, attemptID)
	if err != nil {
		return nil, nil, err
	}
	if a.TaskID != t.ID || a.ExecutionID != e.ID || a.LeaseID != leaseID ||
		(workerID != "" && a.WorkerID != workerID) ||
		a.Outcome != nil || t.LeaseID == nil || *t.LeaseID != leaseID || t.LeaseExpired ||
		(t.State != "assigned" && t.State != "running") {
		return nil, nil, ErrStaleLease
	}
	return e, t, nil
}

func (s *Service) createDelegatedChild(ctx context.Context, tx pgx.Tx, parent *execution, parentTask *task,
	policy DelegationPolicy, delegationID string, req *workerv1.DelegateTaskRequest, depth int) (string, error) {
	var versionID, status string
	var runtimeABI int
	var specJSON []byte
	if err := tx.QueryRow(ctx, `
		SELECT id, status, runtime_abi, spec FROM control.agent_versions
		WHERE org_id = $1 AND project_id = $2 AND agent_id = $3 AND version = $4`,
		parent.OrgID, parent.ProjectID, req.GetAgentId(), req.GetAgentVersion()).
		Scan(&versionID, &status, &runtimeABI, &specJSON); err != nil {
		return "", fmt.Errorf("delegated agent version: %w", err)
	}
	if status != "published" && status != "deprecated" {
		return "", errors.New("delegated agent version is not published")
	}
	var spec struct {
		Model struct {
			Provider string `json:"provider"`
			Name     string `json:"name"`
		} `json:"model"`
		Runtime struct {
			ImageDigest string `json:"image_digest"`
		} `json:"runtime"`
		OutputSchema  *map[string]any `json:"output_schema"`
		Orchestration json.RawMessage `json:"orchestration"`
	}
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return "", errors.New("delegated agent spec is invalid")
	}
	childInput := structMap(req.GetTask())
	if contextValue := structMap(req.GetContext()); len(contextValue) > 0 {
		childInput["context"] = contextValue
	}
	if len(req.GetContextRefs()) > 0 {
		childInput["context_refs"] = req.GetContextRefs()
	}
	inputJSON, err := json.Marshal(childInput)
	if err != nil || len(inputJSON) > maxChildInputBytes {
		return "", errors.New("delegated input exceeds 256 KiB")
	}
	inputDigest, err := contracts.Digest(childInput)
	if err != nil {
		return "", err
	}
	childID := idgen.New("exec")
	requestID := "delegate:" + delegationID
	requestDigest, err := contracts.Digest(struct {
		Parent string         `json:"parent"`
		Agent  string         `json:"agent"`
		Input  map[string]any `json:"input"`
	}{parent.ID, req.GetAgentId() + ":" + strconv.Itoa(int(req.GetAgentVersion())), childInput})
	if err != nil {
		return "", err
	}
	deadline := parent.DeadlineAt
	if policy.MaxDurationSeconds > 0 {
		limit := time.Now().UTC().Add(time.Duration(policy.MaxDurationSeconds) * time.Second)
		if deadline == nil || limit.Before(*deadline) {
			deadline = &limit
		}
	}
	childPolicy := json.RawMessage("{}")
	if len(spec.Orchestration) > 4 && string(spec.Orchestration) != "null" {
		childPolicy = spec.Orchestration
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO execution.executions
			(id, org_id, project_id, workflow_id, workflow_version_id, state, state_version, priority,
			 request_id, request_digest, input, input_digest, created_by, deadline_at, next_event_seq,
			 parent_execution_id, parent_task_id, iteration, delegation_policy, delegation_depth)
		VALUES ($1, $2, $3, $4, $5, 'pending', 1, $6, $7, $8, $9::jsonb, $10, 'orchestrator', $11, 2,
		        $12, $13, 0, $14::jsonb, $15)`,
		childID, parent.OrgID, parent.ProjectID, parent.WorkflowID, parent.WorkflowVersionID,
		parentTask.Priority, requestID, requestDigest, inputJSON, inputDigest, deadline,
		parent.ID, parentTask.ID, childPolicy, depth); err != nil {
		return "", err
	}
	nodeID := "delegate_" + strings.ReplaceAll(strings.TrimPrefix(delegationID, "dlg_"), "-", "")
	if len(nodeID) > 64 {
		nodeID = nodeID[:64]
	}
	maxAttempts := policy.MaxRetries + 1
	if maxAttempts > 10 {
		maxAttempts = 10
	}
	timeoutSeconds := parentTask.TimeoutSeconds
	if req.GetTimeoutSeconds() > 0 {
		timeoutSeconds = int(req.GetTimeoutSeconds())
	}
	if timeoutSeconds < 1 || timeoutSeconds > 86400 {
		timeoutSeconds = 600
	}
	outputSchema := spec.OutputSchema
	if req.GetOutputSchema() != nil {
		value := req.GetOutputSchema().AsMap()
		outputSchema = &value
	}
	var outputSchemaJSON []byte
	if outputSchema != nil {
		outputSchemaJSON, _ = json.Marshal(*outputSchema)
	}
	var runtimeDigest, provider, model *string
	if spec.Runtime.ImageDigest != "" {
		value := spec.Runtime.ImageDigest
		runtimeDigest = &value
	}
	if spec.Model.Provider != "" {
		value := spec.Model.Provider
		provider = &value
	}
	if spec.Model.Name != "" {
		value := spec.Model.Name
		model = &value
	}
	taskID := idgen.New("task")
	if _, err := tx.Exec(ctx, `
		INSERT INTO execution.execution_tasks
			(id, execution_id, org_id, node_id, node_type, agent_version_id, worker_pool, priority,
			 runtime_abi, runtime_digest, provider, model, state, attempt_count, max_attempts,
			 timeout_seconds, side_effect_class, retry_policy, idempotency_key, config, input_from,
			 params, resources, join_policy, output_schema)
		VALUES ($1, $2, $3, $4, 'agent', $5, $6, $7, $8, $9, $10, $11, 'pending', 0, $12, $13,
		        'read_only', '{}'::jsonb, $14, '{}'::jsonb, '[]'::jsonb, '{}'::jsonb, '{}'::jsonb,
		        '{}'::jsonb, $15::jsonb)`,
		taskID, childID, parent.OrgID, nodeID, versionID, parentTask.WorkerPool, parentTask.Priority,
		runtimeABI, runtimeDigest, provider, model, maxAttempts, timeoutSeconds, delegationID,
		outputSchemaJSON); err != nil {
		return "", err
	}
	if err := s.insertChildCreated(ctx, tx, parent, childID, parent.WorkflowVersionID, 1); err != nil {
		return "", err
	}
	return childID, nil
}

// GetDelegation returns the current state/output of a delegation owned by the calling supervisor attempt.
func (s *Service) GetDelegation(ctx context.Context, workerID string,
	req *workerv1.GetDelegationRequest) (*workerv1.GetDelegationResponse, error) {
	var response *workerv1.GetDelegationResponse
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if _, _, err := s.fencedParent(ctx, tx, workerID, req.GetTaskId(), req.GetAttemptId(), req.GetLeaseId()); err != nil {
			return err
		}
		var record delegationRecord
		if err := tx.QueryRow(ctx, `
			SELECT id, root_execution_id, parent_execution_id, parent_task_id, child_execution_id,
			       agent_id, agent_version, status
			FROM execution.delegations
			WHERE id = $1 AND parent_task_id = $2`, req.GetDelegationId(), req.GetTaskId()).
			Scan(&record.ID, &record.RootExecutionID, &record.ParentExecutionID, &record.ParentTaskID,
				&record.ChildExecutionID, &record.AgentID, &record.AgentVersion, &record.Status); err != nil {
			return err
		}
		var childState, failureCode, failureDetail string
		if err := tx.QueryRow(ctx, `
			SELECT state, COALESCE(failure_code, ''), COALESCE(failure_detail, '')
			FROM execution.executions WHERE id = $1`, record.ChildExecutionID).
			Scan(&childState, &failureCode, &failureDetail); err != nil {
			return err
		}
		status := childState
		output := map[string]any{}
		switch childState {
		case "succeeded":
			raw, err := s.childOutput(ctx, tx, record.ChildExecutionID, nil)
			if err != nil {
				return err
			}
			output = singleDelegatedOutput(raw)
			status = "succeeded"
		case "failed", "cancelled":
			status = "failed"
		case "running":
			status = "running"
		}
		if status != record.Status && (status == "succeeded" || status == "failed" || status == "running") {
			resultJSON, _ := json.Marshal(output)
			_, _ = tx.Exec(ctx, `
				UPDATE execution.delegations
				SET status = $2, result_inline = CASE WHEN $2 = 'succeeded' THEN $3::jsonb ELSE result_inline END,
				    error_code = CASE WHEN $2 = 'failed' THEN NULLIF($4, '') ELSE error_code END,
				    error_message = CASE WHEN $2 = 'failed' THEN NULLIF($5, '') ELSE error_message END,
				    finished_at = CASE WHEN $2 IN ('succeeded', 'failed') THEN now() ELSE finished_at END
				WHERE id = $1`, record.ID, status, resultJSON, failureCode, failureDetail)
		}
		var tokensIn, tokensOut int64
		_ = tx.QueryRow(ctx, `
			SELECT COALESCE(sum(COALESCE((metrics->>'tokens_in')::bigint, 0)), 0),
			       COALESCE(sum(COALESCE((metrics->>'tokens_out')::bigint, 0)), 0)
			FROM execution.task_attempts WHERE execution_id = $1`, record.ChildExecutionID).
			Scan(&tokensIn, &tokensOut)
		response = &workerv1.GetDelegationResponse{
			DelegationId: record.ID, ChildExecutionId: record.ChildExecutionID, Status: status,
			Output: mustStruct(output), ErrorCode: failureCode, ErrorMessage: failureDetail,
			TokensIn: tokensIn, TokensOut: tokensOut,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func singleDelegatedOutput(output map[string]any) map[string]any {
	if len(output) == 1 {
		for _, value := range output {
			if result, ok := value.(map[string]any); ok {
				return result
			}
		}
	}
	if value, ok := output["output"].(map[string]any); ok {
		return value
	}
	return output
}

// ListDelegationAgents returns the frozen supervisor allowlist and remaining budget.
func (s *Service) ListDelegationAgents(ctx context.Context, workerID string,
	req *workerv1.ListDelegationAgentsRequest) (*workerv1.ListDelegationAgentsResponse, error) {
	var response *workerv1.ListDelegationAgentsResponse
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		parent, parentTask, err := s.fencedParent(ctx, tx, workerID, req.GetTaskId(), req.GetAttemptId(), req.GetLeaseId())
		if err != nil {
			return err
		}
		policy, err := delegationPolicyForTask(ctx, tx, parentTask)
		if err != nil {
			return err
		}
		rootID, _, err := delegationRoot(ctx, tx, parent.ID)
		if err != nil {
			return err
		}
		var children int
		_ = tx.QueryRow(ctx, `SELECT count(*) FROM execution.delegations WHERE root_execution_id = $1`, rootID).Scan(&children)
		agents := make([]*workerv1.DelegationAgent, 0, len(policy.AllowedAgents))
		for _, allowed := range policy.AllowedAgents {
			agents = append(agents, &workerv1.DelegationAgent{
				AgentId: allowed.AgentID, Version: int32(allowed.Version), //nolint:gosec // bounded by agent validation.
				Name: allowed.Name,
			})
		}
		response = &workerv1.ListDelegationAgentsResponse{
			Agents:            agents,
			MaxParallel:       int32(policy.MaxParallel),                  //nolint:gosec // bounded by policy validation.
			RemainingChildren: int32(max(policy.MaxChildren-children, 0)), //nolint:gosec // bounded by policy validation.
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// PublishAgentEvent stores a durable message visible to the execution hierarchy.
func (s *Service) PublishAgentEvent(ctx context.Context, workerID string,
	req *workerv1.PublishAgentEventRequest) (*workerv1.PublishAgentEventResponse, error) {
	if strings.TrimSpace(req.GetTopic()) == "" {
		return nil, errors.New("event topic is required")
	}
	var eventID string
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		parent, task, err := s.fencedParent(ctx, tx, workerID, req.GetTaskId(), req.GetAttemptId(), req.GetLeaseId())
		if err != nil {
			return err
		}
		rootID, _, err := delegationRoot(ctx, tx, parent.ID)
		if err != nil {
			return err
		}
		eventID = idgen.New("evtmsg")
		payload, _ := json.Marshal(structMap(req.GetPayload()))
		_, err = tx.Exec(ctx, `
			INSERT INTO execution.agent_events
				(id, org_id, root_execution_id, execution_id, task_id, topic, payload)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)`,
			eventID, parent.OrgID, rootID, parent.ID, task.ID, req.GetTopic(), payload)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &workerv1.PublishAgentEventResponse{EventId: eventID}, nil
}

// WaitAgentEvent long-polls for the next unconsumed event on a topic.
func (s *Service) WaitAgentEvent(ctx context.Context, workerID string,
	req *workerv1.WaitAgentEventRequest) (*workerv1.WaitAgentEventResponse, error) {
	if req.GetTopic() == "" {
		return nil, errors.New("event topic is required")
	}
	var rootID, consumerID string
	if err := s.withTx(ctx, func(tx pgx.Tx) error {
		parent, _, err := s.fencedParent(ctx, tx, workerID, req.GetTaskId(), req.GetAttemptId(), req.GetLeaseId())
		if err != nil {
			return err
		}
		consumerID = parent.ID
		rootID, _, err = delegationRoot(ctx, tx, parent.ID)
		return err
	}); err != nil {
		return nil, err
	}
	timeout := time.Duration(req.GetTimeoutSeconds()) * time.Second
	if timeout <= 0 || timeout > 10*time.Minute {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var eventID string
		var payload []byte
		err := s.pool.QueryRow(ctx, `
			UPDATE execution.agent_events
			SET consumed_by_execution_id = $3, consumed_at = now()
			WHERE id = (
				SELECT id FROM execution.agent_events
				WHERE root_execution_id = $1 AND topic = $2 AND consumed_by_execution_id IS NULL
				ORDER BY created_at LIMIT 1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING id, payload`, rootID, req.GetTopic(), consumerID).
			Scan(&eventID, &payload)
		if err == nil {
			value := map[string]any{}
			_ = json.Unmarshal(payload, &value)
			return &workerv1.WaitAgentEventResponse{EventId: eventID, Payload: mustStruct(value), Found: true}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return &workerv1.WaitAgentEventResponse{Found: false}, nil
}

func delegationRoot(ctx context.Context, tx pgx.Tx, executionID string) (string, int, error) {
	var rootID string
	var depth int
	err := tx.QueryRow(ctx, `
		WITH RECURSIVE chain AS (
			SELECT id, parent_execution_id, 0 AS depth FROM execution.executions WHERE id = $1
			UNION ALL
			SELECT e.id, e.parent_execution_id, c.depth + 1
			FROM execution.executions e JOIN chain c ON e.id = c.parent_execution_id
			WHERE c.depth < 64
		)
		SELECT id, depth FROM chain ORDER BY depth DESC LIMIT 1`, executionID).Scan(&rootID, &depth)
	return rootID, depth, err
}

func structMap(value interface{ AsMap() map[string]any }) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value.AsMap()
}

func mustStruct(value map[string]any) *structpb.Struct {
	converted, err := structpb.NewStruct(value)
	if err != nil {
		return &structpb.Struct{}
	}
	return converted
}
