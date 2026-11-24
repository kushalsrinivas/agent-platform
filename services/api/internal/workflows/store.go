package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/packages/contracts"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
)

var (
	ErrNotFound            = errors.New("workflow not found")
	ErrVersionNotFound     = errors.New("workflow version not found")
	ErrVersionState        = errors.New("workflow version cannot transition from its current status")
	ErrAgentUnavailable    = errors.New("a referenced agent version is missing or not published")
	ErrWorkflowUnavailable = errors.New("a referenced subworkflow version is missing or not published")
)

type Workflow struct {
	ID              string    `json:"id"`
	OrgID           string    `json:"org_id"`
	ProjectID       string    `json:"project_id"`
	Name            string    `json:"name"`
	Description     *string   `json:"description"`
	LatestVersion   int       `json:"latest_version"`
	DeployedVersion *int      `json:"deployed_version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Version struct {
	ID          string          `json:"id"`
	WorkflowID  string          `json:"workflow_id"`
	Version     int             `json:"version"`
	Status      string          `json:"status"`
	Digest      string          `json:"digest"`
	Graph       json.RawMessage `json:"graph"`
	CreatedBy   string          `json:"created_by"`
	CreatedAt   time.Time       `json:"created_at"`
	PublishedAt *time.Time      `json:"published_at"`
}

type Store struct {
	db *database.DB
}

func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

func (s *Store) List(ctx context.Context, orgID, projectID string, limit int, cursor string) ([]Workflow, string, error) {
	workflows := make([]Workflow, 0, limit)
	var nextCursor string
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		query := `
			SELECT id, org_id, project_id, name, description, latest_version, deployed_version, created_at, updated_at
			FROM control.workflows
			WHERE org_id = $1 AND project_id = $2`
		args := []any{orgID, projectID}
		if cursor != "" {
			parts, err := database.DecodeCursor(cursor)
			if err != nil || len(parts) != 2 {
				return errors.New("invalid cursor")
			}
			query += ` AND (created_at, id) < ($3::timestamptz, $4)`
			args = append(args, parts[0], parts[1])
		}
		args = append(args, limit+1)
		query += ` ORDER BY created_at DESC, id DESC LIMIT $` + itoa(len(args))
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var workflow Workflow
			var description string
			if err := rows.Scan(&workflow.ID, &workflow.OrgID, &workflow.ProjectID, &workflow.Name,
				&description, &workflow.LatestVersion, &workflow.DeployedVersion,
				&workflow.CreatedAt, &workflow.UpdatedAt); err != nil {
				return err
			}
			if description != "" {
				workflow.Description = &description
			}
			workflows = append(workflows, workflow)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(workflows) > limit {
			workflows = workflows[:limit]
			last := workflows[limit-1]
			nextCursor = database.EncodeCursor(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID)
		}
		return nil
	})
	return workflows, nextCursor, err
}

func (s *Store) Get(ctx context.Context, orgID, projectID, workflowID string) (Workflow, error) {
	var workflow Workflow
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		var description string
		err := tx.QueryRow(ctx, `
			SELECT id, org_id, project_id, name, description, latest_version, deployed_version, created_at, updated_at
			FROM control.workflows
			WHERE org_id = $1 AND project_id = $2 AND id = $3`, orgID, projectID, workflowID).
			Scan(&workflow.ID, &workflow.OrgID, &workflow.ProjectID, &workflow.Name, &description,
				&workflow.LatestVersion, &workflow.DeployedVersion, &workflow.CreatedAt, &workflow.UpdatedAt)
		if description != "" {
			workflow.Description = &description
		}
		return err
	})
	if database.IsNotFound(err) {
		return Workflow{}, ErrNotFound
	}
	return workflow, err
}

func (s *Store) ListVersions(ctx context.Context, orgID, projectID, workflowID string, limit int) ([]Version, error) {
	versions := make([]Version, 0, limit)
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, workflow_id, version, status, digest, graph, created_by, created_at, published_at
			FROM control.workflow_versions
			WHERE org_id = $1 AND project_id = $2 AND workflow_id = $3
			ORDER BY version DESC
			LIMIT $4`, orgID, projectID, workflowID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var version Version
			if err := rows.Scan(&version.ID, &version.WorkflowID, &version.Version, &version.Status,
				&version.Digest, &version.Graph, &version.CreatedBy, &version.CreatedAt, &version.PublishedAt); err != nil {
				return err
			}
			versions = append(versions, version)
		}
		return rows.Err()
	})
	return versions, err
}

func (s *Store) GetVersion(ctx context.Context, orgID, projectID, workflowID string, version int) (Version, error) {
	var result Version
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, workflow_id, version, status, digest, graph, created_by, created_at, published_at
			FROM control.workflow_versions
			WHERE org_id = $1 AND project_id = $2 AND workflow_id = $3 AND version = $4`,
			orgID, projectID, workflowID, version).
			Scan(&result.ID, &result.WorkflowID, &result.Version, &result.Status, &result.Digest,
				&result.Graph, &result.CreatedBy, &result.CreatedAt, &result.PublishedAt)
	})
	if database.IsNotFound(err) {
		return Version{}, ErrVersionNotFound
	}
	return result, err
}

func (s *Store) Create(ctx context.Context, orgID, projectID, name, description, principalID string,
	graph contracts.WorkflowGraph) (Workflow, error) {
	if err := graph.Validate(); err != nil {
		return Workflow{}, err
	}
	if err := validateEmbeddedSchemas(&graph); err != nil {
		return Workflow{}, err
	}
	digest, err := graph.Digest()
	if err != nil {
		return Workflow{}, err
	}
	graphJSON, err := json.Marshal(graph)
	if err != nil {
		return Workflow{}, err
	}

	workflow := Workflow{
		ID:            idgen.New("wf"),
		OrgID:         orgID,
		ProjectID:     projectID,
		Name:          name,
		LatestVersion: 1,
	}
	if description != "" {
		workflow.Description = &description
	}
	err = s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		if err := validateAgentRefs(ctx, tx, orgID, projectID, &graph, false); err != nil {
			return err
		}
		if err := validateSubworkflowRefs(ctx, tx, orgID, projectID, workflow.ID, &graph, false); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO control.workflows (id, org_id, project_id, name, description, latest_version)
			VALUES ($1, $2, $3, $4, $5, 1)
			RETURNING created_at, updated_at`, workflow.ID, orgID, projectID, name, description).
			Scan(&workflow.CreatedAt, &workflow.UpdatedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO control.workflow_versions
				(id, workflow_id, org_id, project_id, version, status, digest, graph, created_by)
			VALUES ($1, $2, $3, $4, 1, 'draft', $5, $6, $7)`,
			idgen.New("wfv"), workflow.ID, orgID, projectID, digest, graphJSON, principalID)
		return err
	})
	return workflow, err
}

func (s *Store) CreateVersion(ctx context.Context, orgID, projectID, workflowID, principalID string,
	graph contracts.WorkflowGraph) (Version, error) {
	if err := graph.Validate(); err != nil {
		return Version{}, err
	}
	if err := validateEmbeddedSchemas(&graph); err != nil {
		return Version{}, err
	}
	digest, err := graph.Digest()
	if err != nil {
		return Version{}, err
	}
	graphJSON, err := json.Marshal(graph)
	if err != nil {
		return Version{}, err
	}

	var version Version
	err = s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		var latest int
		if err := tx.QueryRow(ctx, `
			SELECT latest_version FROM control.workflows
			WHERE org_id = $1 AND project_id = $2 AND id = $3
			FOR UPDATE`, orgID, projectID, workflowID).Scan(&latest); err != nil {
			return err
		}
		if err := validateAgentRefs(ctx, tx, orgID, projectID, &graph, false); err != nil {
			return err
		}
		if err := validateSubworkflowRefs(ctx, tx, orgID, projectID, workflowID, &graph, false); err != nil {
			return err
		}
		version = Version{
			ID:         idgen.New("wfv"),
			WorkflowID: workflowID,
			Version:    latest + 1,
			Status:     "draft",
			Digest:     digest,
			CreatedBy:  principalID,
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO control.workflow_versions
				(id, workflow_id, org_id, project_id, version, status, digest, graph, created_by)
			VALUES ($1, $2, $3, $4, $5, 'draft', $6, $7, $8)
			RETURNING created_at`, version.ID, workflowID, orgID, projectID, version.Version,
			digest, graphJSON, principalID).Scan(&version.CreatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE control.workflows SET latest_version = $4
			WHERE org_id = $1 AND project_id = $2 AND id = $3`,
			orgID, projectID, workflowID, version.Version); err != nil {
			return err
		}
		version.Graph = graphJSON
		return nil
	})
	if database.IsNotFound(err) {
		return Version{}, ErrNotFound
	}
	return version, err
}

func (s *Store) Publish(ctx context.Context, orgID, projectID, workflowID string, version int) (Version, error) {
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		var status string
		var graphJSON []byte
		if err := tx.QueryRow(ctx, `
			SELECT status, graph FROM control.workflow_versions
			WHERE org_id = $1 AND project_id = $2 AND workflow_id = $3 AND version = $4
			FOR UPDATE`, orgID, projectID, workflowID, version).Scan(&status, &graphJSON); err != nil {
			return err
		}
		if status == "published" {
			_, err := tx.Exec(ctx, `
				UPDATE control.workflows SET deployed_version = $4, updated_at = now()
				WHERE org_id = $1 AND project_id = $2 AND id = $3`,
				orgID, projectID, workflowID, version)
			return err
		}
		if status != "draft" {
			return ErrVersionState
		}
		var graph contracts.WorkflowGraph
		if err := json.Unmarshal(graphJSON, &graph); err != nil {
			return err
		}
		if err := graph.Validate(); err != nil {
			return err
		}
		if err := validateEmbeddedSchemas(&graph); err != nil {
			return err
		}
		if err := validateAgentRefs(ctx, tx, orgID, projectID, &graph, true); err != nil {
			return err
		}
		if err := validateSubworkflowRefs(ctx, tx, orgID, projectID, workflowID, &graph, true); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE control.workflow_versions SET status = 'published', published_at = now()
			WHERE org_id = $1 AND project_id = $2 AND workflow_id = $3 AND version = $4`,
			orgID, projectID, workflowID, version); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE control.workflows SET deployed_version = $4, updated_at = now()
			WHERE org_id = $1 AND project_id = $2 AND id = $3`,
			orgID, projectID, workflowID, version)
		return err
	})
	if database.IsNotFound(err) {
		return Version{}, ErrVersionNotFound
	}
	if err != nil {
		return Version{}, err
	}
	return s.GetVersion(ctx, orgID, projectID, workflowID, version)
}

func validateAgentRefs(ctx context.Context, tx pgx.Tx, orgID, projectID string, graph *contracts.WorkflowGraph,
	requirePublished bool) error {
	for _, node := range graph.Nodes {
		if node.Agent == nil {
			continue
		}
		var status string
		err := tx.QueryRow(ctx, `
			SELECT status FROM control.agent_versions
			WHERE org_id = $1 AND project_id = $2 AND agent_id = $3 AND version = $4`,
			orgID, projectID, node.Agent.AgentID, node.Agent.Version).Scan(&status)
		if database.IsNotFound(err) {
			return ErrAgentUnavailable
		}
		if err != nil {
			return err
		}
		if requirePublished && status != "published" {
			return ErrAgentUnavailable
		}
	}
	return nil
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[pos:])
}

func validateSubworkflowRefs(ctx context.Context, tx pgx.Tx, orgID, projectID, workflowID string,
	graph *contracts.WorkflowGraph, requirePublished bool) error {
	for _, node := range graph.Nodes {
		if node.Config == nil || node.Config.Workflow == nil {
			continue
		}
		if node.Config.Workflow.WorkflowID == workflowID && requirePublished {
			return ErrWorkflowUnavailable
		}
		var status string
		err := tx.QueryRow(ctx, `
			SELECT status FROM control.workflow_versions
			WHERE org_id = $1 AND project_id = $2 AND workflow_id = $3 AND version = $4`,
			orgID, projectID, node.Config.Workflow.WorkflowID, node.Config.Workflow.Version).Scan(&status)
		if database.IsNotFound(err) {
			return ErrWorkflowUnavailable
		}
		if err != nil {
			return err
		}
		if requirePublished && status != "published" {
			return ErrWorkflowUnavailable
		}
	}
	return nil
}
