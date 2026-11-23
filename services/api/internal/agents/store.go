package agents

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
)

var (
	ErrNotFound        = errors.New("agent not found")
	ErrVersionNotFound = errors.New("agent version not found")
	ErrVersionState    = errors.New("agent version cannot transition from its current status")
)

type Agent struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"org_id"`
	ProjectID     string    `json:"project_id"`
	Name          string    `json:"name"`
	Description   *string   `json:"description"`
	LatestVersion int       `json:"latest_version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Version struct {
	ID          string          `json:"id"`
	AgentID     string          `json:"agent_id"`
	Version     int             `json:"version"`
	Status      string          `json:"status"`
	Digest      string          `json:"digest"`
	Spec        json.RawMessage `json:"spec"`
	RuntimeABI  int             `json:"runtime_abi"`
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

func (s *Store) List(ctx context.Context, orgID, projectID string, limit int, cursor string) ([]Agent, string, error) {
	agents := make([]Agent, 0, limit)
	var nextCursor string
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		query := `
			SELECT id, org_id, project_id, name, description, latest_version, created_at, updated_at
			FROM control.agents
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
			var agent Agent
			var description string
			if err := rows.Scan(&agent.ID, &agent.OrgID, &agent.ProjectID, &agent.Name, &description,
				&agent.LatestVersion, &agent.CreatedAt, &agent.UpdatedAt); err != nil {
				return err
			}
			if description != "" {
				agent.Description = &description
			}
			agents = append(agents, agent)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(agents) > limit {
			agents = agents[:limit]
			last := agents[limit-1]
			nextCursor = database.EncodeCursor(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID)
		}
		return nil
	})
	return agents, nextCursor, err
}

func (s *Store) Get(ctx context.Context, orgID, projectID, agentID string) (Agent, error) {
	var agent Agent
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		var description string
		err := tx.QueryRow(ctx, `
			SELECT id, org_id, project_id, name, description, latest_version, created_at, updated_at
			FROM control.agents
			WHERE org_id = $1 AND project_id = $2 AND id = $3`, orgID, projectID, agentID).
			Scan(&agent.ID, &agent.OrgID, &agent.ProjectID, &agent.Name, &description,
				&agent.LatestVersion, &agent.CreatedAt, &agent.UpdatedAt)
		if description != "" {
			agent.Description = &description
		}
		return err
	})
	if database.IsNotFound(err) {
		return Agent{}, ErrNotFound
	}
	return agent, err
}

func (s *Store) ListVersions(ctx context.Context, orgID, projectID, agentID string, limit int, cursor int) ([]Version, error) {
	versions := make([]Version, 0, limit)
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, agent_id, version, status, digest, spec, runtime_abi, created_by, created_at, published_at
			FROM control.agent_versions
			WHERE org_id = $1 AND project_id = $2 AND agent_id = $3 AND ($4 = 0 OR version < $4)
			ORDER BY version DESC
			LIMIT $5`, orgID, projectID, agentID, cursor, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var version Version
			if err := rows.Scan(&version.ID, &version.AgentID, &version.Version, &version.Status,
				&version.Digest, &version.Spec, &version.RuntimeABI, &version.CreatedBy,
				&version.CreatedAt, &version.PublishedAt); err != nil {
				return err
			}
			versions = append(versions, version)
		}
		return rows.Err()
	})
	return versions, err
}

func (s *Store) GetVersion(ctx context.Context, orgID, projectID, agentID string, version int) (Version, error) {
	var result Version
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, agent_id, version, status, digest, spec, runtime_abi, created_by, created_at, published_at
			FROM control.agent_versions
			WHERE org_id = $1 AND project_id = $2 AND agent_id = $3 AND version = $4`,
			orgID, projectID, agentID, version).
			Scan(&result.ID, &result.AgentID, &result.Version, &result.Status, &result.Digest,
				&result.Spec, &result.RuntimeABI, &result.CreatedBy, &result.CreatedAt, &result.PublishedAt)
	})
	if database.IsNotFound(err) {
		return Version{}, ErrVersionNotFound
	}
	return result, err
}

func (s *Store) Create(ctx context.Context, orgID, projectID, name, description, principalID string, spec AgentSpec) (Agent, error) {
	digest, err := spec.Digest()
	if err != nil {
		return Agent{}, err
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return Agent{}, err
	}
	agent := Agent{ID: idgen.New("agt"), OrgID: orgID, ProjectID: projectID, Name: name, LatestVersion: 1}
	if description != "" {
		agent.Description = &description
	}
	err = s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO control.agents (id, org_id, project_id, name, description, latest_version)
			VALUES ($1, $2, $3, $4, $5, 1)
			RETURNING created_at, updated_at`, agent.ID, orgID, projectID, name, description).
			Scan(&agent.CreatedAt, &agent.UpdatedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO control.agent_versions
				(id, agent_id, org_id, project_id, version, status, digest, spec, runtime_abi, created_by)
			VALUES ($1, $2, $3, $4, 1, 'draft', $5, $6, $7, $8)`,
			idgen.New("agtv"), agent.ID, orgID, projectID, digest, specJSON, spec.Runtime.ABIVersion, principalID)
		return err
	})
	return agent, err
}

func (s *Store) CreateVersion(ctx context.Context, orgID, projectID, agentID, principalID string, spec AgentSpec) (Version, error) {
	digest, err := spec.Digest()
	if err != nil {
		return Version{}, err
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return Version{}, err
	}
	var version Version
	err = s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		var latest int
		if err := tx.QueryRow(ctx, `
			SELECT latest_version FROM control.agents
			WHERE org_id = $1 AND project_id = $2 AND id = $3
			FOR UPDATE`, orgID, projectID, agentID).Scan(&latest); err != nil {
			return err
		}
		version = Version{
			ID:         idgen.New("agtv"),
			AgentID:    agentID,
			Version:    latest + 1,
			Status:     "draft",
			Digest:     digest,
			RuntimeABI: spec.Runtime.ABIVersion,
			CreatedBy:  principalID,
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO control.agent_versions
				(id, agent_id, org_id, project_id, version, status, digest, spec, runtime_abi, created_by)
			VALUES ($1, $2, $3, $4, $5, 'draft', $6, $7, $8, $9)
			RETURNING created_at`, version.ID, agentID, orgID, projectID, version.Version,
			version.Digest, specJSON, version.RuntimeABI, principalID).Scan(&version.CreatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE control.agents SET latest_version = $4
			WHERE org_id = $1 AND project_id = $2 AND id = $3`,
			orgID, projectID, agentID, version.Version); err != nil {
			return err
		}
		version.Spec = specJSON
		return nil
	})
	if database.IsNotFound(err) {
		return Version{}, ErrNotFound
	}
	return version, err
}

func (s *Store) Publish(ctx context.Context, orgID, projectID, agentID string, version int) (Version, error) {
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `
			SELECT status FROM control.agent_versions
			WHERE org_id = $1 AND project_id = $2 AND agent_id = $3 AND version = $4
			FOR UPDATE`, orgID, projectID, agentID, version).Scan(&status); err != nil {
			return err
		}
		if status == "published" {
			return nil
		}
		if status != "draft" {
			return ErrVersionState
		}
		_, err := tx.Exec(ctx, `
			UPDATE control.agent_versions SET status = 'published', published_at = now()
			WHERE org_id = $1 AND project_id = $2 AND agent_id = $3 AND version = $4`,
			orgID, projectID, agentID, version)
		return err
	})
	if database.IsNotFound(err) {
		return Version{}, ErrVersionNotFound
	}
	if err != nil {
		return Version{}, err
	}
	return s.GetVersion(ctx, orgID, projectID, agentID, version)
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
