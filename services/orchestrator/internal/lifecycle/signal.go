package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agent-platform/agent-platform/packages/contracts"
)

// SignalResult is the durable outcome of an approval or delay signal.
type SignalResult struct {
	TaskID       string
	NodeType     string
	ControlState map[string]any
	Accepted     bool
}

// SignalTask applies a human decision to an approval node or an external payload to a delay node.
func (s *Service) SignalTask(ctx context.Context, orgID, projectID, executionID, taskID, action,
	comment, actor string, payload map[string]any) (SignalResult, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	if action != "approve" && action != "reject" && action != "signal" {
		return SignalResult{}, errors.New("action must be approve, reject or signal")
	}
	if len(comment) > 2000 {
		comment = comment[:2000]
	}
	if actor == "" {
		actor = "operator"
	}
	result := SignalResult{TaskID: taskID}
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		e, err := s.lockExecutionForTask(ctx, tx, taskID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if e.OrgID != orgID || e.ProjectID != projectID || e.ID != executionID {
			return ErrNotFound
		}
		if terminalState(e.State) || e.State == "failing" || e.State == "cancel_requested" {
			return fmt.Errorf("execution is not accepting signals")
		}
		t, err := s.lockTask(ctx, tx, taskID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if t.State != "pending" {
			return fmt.Errorf("task is not waiting")
		}
		result.NodeType = t.NodeType
		state := map[string]any{}
		for key, value := range t.ControlState {
			state[key] = value
		}
		reason := ""
		switch action {
		case "approve", "reject":
			if t.NodeType != "approval" {
				return fmt.Errorf("task %s is not an approval node", t.ID)
			}
			status, _ := state["status"].(string)
			if status != "" && status != "pending" {
				return fmt.Errorf("approval was already decided")
			}
			config := approvalConfigForTask(t)
			approvers := stringSlice(state["approvers"])
			if len(approvers) > 0 && !containsString(approvers, actor) {
				return fmt.Errorf("actor %s is not an assigned approver", actor)
			}
			if action == "reject" && config.Approval != nil && config.Approval.RejectReasonRequired != nil &&
				*config.Approval.RejectReasonRequired && strings.TrimSpace(comment) == "" {
				return errors.New("a rejection reason is required")
			}
			decisions := map[string]any{}
			if existing, ok := state["decisions"].(map[string]any); ok {
				for key, value := range existing {
					decisions[key] = value
				}
			}
			if prior, ok := decisions[actor].(map[string]any); ok {
				if prior["decision"] == action {
					result.ControlState = state
					result.Accepted = true
					return nil
				}
				return errors.New("actor has already voted")
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			decisions[actor] = map[string]any{
				"decision": action, "comment": comment, "at": now,
			}
			state["decisions"] = decisions
			state["by"] = actor
			state["comment"] = comment
			state["reason"] = comment
			state["at"] = now
			policy, _ := state["policy"].(string)
			next := "approved"
			if action == "reject" {
				next = "rejected"
			}
			if policy == "all" && action == "approve" {
				next = "pending"
				if len(approvers) > 0 {
					all := true
					for _, approver := range approvers {
						value, _ := decisions[approver].(map[string]any)
						if value == nil || value["decision"] != "approve" {
							all = false
							break
						}
					}
					if all {
						next = "approved"
					}
				}
			}
			state["status"] = next
			if err := s.recordApprovalDecision(ctx, tx, t, actor, action, comment); err != nil {
				return err
			}
			if next == "pending" {
				reason = "approval_vote"
			} else {
				reason = "approval_" + next
				if err := s.finishApproval(ctx, tx, t, state); err != nil {
					return err
				}
			}
		case "signal":
			if t.NodeType != "delay" {
				return fmt.Errorf("task %s is not a delay node", t.ID)
			}
			state["signal"] = map[string]any{"comment": comment, "actor": actor,
				"payload": payload, "at": time.Now().UTC().Format(time.RFC3339Nano)}
			reason = "delay_signal"
		}
		encoded, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE execution.execution_tasks
			SET control_state = $2::jsonb, next_attempt_at = now(), updated_at = now()
			WHERE id = $1 AND state = 'pending'`, t.ID, encoded); err != nil {
			return err
		}
		from, to := "pending", "pending"
		taskIDValue, nodeID, version := t.ID, t.NodeID, t.StateVersion
		if err := s.appendEvent(ctx, tx, e, eventSpec{
			MessageType: contracts.EventTaskStateChanged,
			EntityType:  "task",
			EntityID:    taskIDValue,
			From:        &from,
			To:          &to,
			ActorType:   "user",
			ActorID:     actor,
			Subject:     taskSubject(e, taskIDValue, "state_changed"),
			TaskID:      &taskIDValue,
			Payload: func(seq int64) any {
				return contracts.TaskStateChangedPayload{
					Seq: seq, TaskID: taskIDValue, NodeID: nodeID,
					From: from, To: to, StateVersion: version, Reason: &reason,
				}
			},
		}); err != nil {
			return err
		}
		result.ControlState = state
		result.Accepted = true
		return nil
	})
	if err != nil {
		return SignalResult{}, err
	}
	return result, nil
}
