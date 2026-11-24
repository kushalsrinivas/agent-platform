package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/packages/contracts"
)

// ensureApproval persists the review row the first time an approval node defers. The task stays pending and
// never occupies a worker slot.
func (s *Service) ensureApproval(ctx context.Context, tx pgx.Tx, e *execution, t *task,
	config contracts.NodeConfig, state map[string]any) error {
	approvalID, _ := state["approval_id"].(string)
	if approvalID == "" {
		approvalID = idgen.New("apr")
		state["approval_id"] = approvalID
	}
	prompt := ""
	if config.Approval != nil && config.Approval.Prompt != nil {
		prompt = *config.Approval.Prompt
	}
	policy, _ := state["policy"].(string)
	if policy != "all" {
		policy = "any"
	}
	approvers := stringSlice(state["approvers"])
	var expiresAt *time.Time
	if raw, _ := state["expires_at"].(string); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			expiresAt = &parsed
		}
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO execution.approvals
			(id, org_id, project_id, execution_id, task_id, prompt, policy, approvers, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $9)
		ON CONFLICT (task_id) DO NOTHING`,
		approvalID, e.OrgID, e.ProjectID, e.ID, t.ID, prompt, policy, approvers, expiresAt)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	from, to := "pending", "pending"
	reason := "approval_requested"
	taskID, nodeID := t.ID, t.NodeID
	return s.appendEvent(ctx, tx, e, eventSpec{
		MessageType: contracts.EventTaskStateChanged,
		EntityType:  "task",
		EntityID:    taskID,
		From:        &from,
		To:          &to,
		ActorType:   "system",
		ActorID:     "orchestrator",
		Subject:     taskSubject(e, taskID, "state_changed"),
		TaskID:      &taskID,
		Payload: func(seq int64) any {
			return contracts.TaskStateChangedPayload{
				Seq: seq, TaskID: taskID, NodeID: nodeID,
				From: from, To: to, StateVersion: t.StateVersion, Reason: &reason,
			}
		},
	})
}

// finishApproval mirrors the terminal control state into the queryable approval audit row.
func (s *Service) finishApproval(ctx context.Context, tx pgx.Tx, t *task, state map[string]any) error {
	status := "approved"
	if value, _ := state["status"].(string); value != "" {
		status = value
	}
	switch status {
	case "approved", "rejected", "expired":
	default:
		return nil
	}
	by, _ := state["by"].(string)
	reason := ""
	if value, _ := state["reason"].(string); value != "" {
		reason = value
	} else if value, _ := state["comment"].(string); value != "" {
		reason = value
	}
	_, err := tx.Exec(ctx, `
		UPDATE execution.approvals
		SET status = $2, decided_at = now(), decided_by = NULLIF($3, ''), reason = NULLIF($4, '')
		WHERE task_id = $1 AND status = 'pending'`, t.ID, status, by, reason)
	if err != nil {
		return err
	}
	if status == "expired" {
		_, err = tx.Exec(ctx, `
			INSERT INTO execution.approval_decisions (id, approval_id, actor, decision, comment)
			SELECT $2, id, 'system', 'expire', COALESCE($3, 'approval expired')
			FROM execution.approvals a
			WHERE a.task_id = $1 AND NOT EXISTS (
				SELECT 1 FROM execution.approval_decisions d
				WHERE d.approval_id = a.id AND d.decision = 'expire'
			)`, t.ID, idgen.New("apd"), reason)
	}
	return err
}

func (s *Service) recordApprovalDecision(ctx context.Context, tx pgx.Tx, t *task,
	actor, decision, comment string) error {
	var approvalID string
	if err := tx.QueryRow(ctx, `SELECT id FROM execution.approvals WHERE task_id = $1`, t.ID).Scan(&approvalID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO execution.approval_decisions (id, approval_id, actor, decision, comment)
		VALUES ($1, $2, $3, $4, $5)`, idgen.New("apd"), approvalID, actor, decision, comment)
	return err
}

func approvalStateFromTask(t *task) map[string]any {
	state := map[string]any{}
	for key, value := range t.ControlState {
		state[key] = value
	}
	return state
}

func approvalConfigForTask(t *task) contracts.NodeConfig {
	config := contracts.NodeConfig{}
	if len(t.Config) > 0 {
		_ = json.Unmarshal(t.Config, &config)
	}
	return config
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func stringSlice(value any) []string {
	switch items := value.(type) {
	case []string:
		return append([]string{}, items...)
	case []any:
		result := make([]string, 0, len(items))
		for _, item := range items {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return []string{}
	}
}
