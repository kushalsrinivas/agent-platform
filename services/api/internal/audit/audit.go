// Package audit appends tenant-scoped administrative events. Rows are write-once: the database rejects UPDATE and
// DELETE for the application roles.
package audit

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
)

type Event struct {
	OrgID        string
	ProjectID    string
	Action       string
	ResourceType string
	ResourceID   string
	Outcome      string
	Details      map[string]any
}

// Record appends an event using the authenticated principal and tenant from the request context.
func Record(ctx context.Context, db *database.DB, event Event) error {
	membership, ok := auth.MembershipFrom(ctx)
	if !ok {
		return nil
	}
	principal, _ := auth.PrincipalFrom(ctx)
	actorType := "user"
	if principal.APIKeyOrgID != "" {
		actorType = "api_key"
	}
	if event.OrgID == "" {
		event.OrgID = membership.OrgID
	}
	if event.Outcome == "" {
		event.Outcome = "success"
	}
	if event.Details == nil {
		event.Details = map[string]any{}
	}
	details, err := json.Marshal(event.Details)
	if err != nil {
		return err
	}
	var projectID any
	if event.ProjectID != "" {
		projectID = event.ProjectID
	}
	return db.WithTenant(ctx, event.OrgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO control.audit_events
				(id, org_id, project_id, actor_type, actor_id, action, resource_type, resource_id, outcome, details)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			idgen.New("aud"), event.OrgID, projectID, actorType, principal.ID, event.Action,
			event.ResourceType, event.ResourceID, event.Outcome, details)
		return err
	})
}
