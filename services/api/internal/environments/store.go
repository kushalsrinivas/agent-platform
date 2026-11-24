// Package environments implements project environment labels for the developer API.
package environments

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
)

var ErrNotFound = errors.New("environment not found")

type Environment struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store struct {
	db *database.DB
}

func NewStore(db *database.DB) *Store { return &Store{db: db} }

func (s *Store) List(ctx context.Context, orgID, projectID string) ([]Environment, error) {
	items := make([]Environment, 0)
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, org_id, project_id, name, slug, kind, created_at, updated_at
			FROM control.environments
			WHERE org_id = $1 AND project_id = $2
			ORDER BY name, id`, orgID, projectID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Environment
			if err := rows.Scan(&item.ID, &item.OrgID, &item.ProjectID, &item.Name, &item.Slug,
				&item.Kind, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (s *Store) Get(ctx context.Context, orgID, projectID, envID string) (Environment, error) {
	var item Environment
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT id, org_id, project_id, name, slug, kind, created_at, updated_at
			FROM control.environments
			WHERE org_id = $1 AND project_id = $2 AND id = $3`, orgID, projectID, envID).
			Scan(&item.ID, &item.OrgID, &item.ProjectID, &item.Name, &item.Slug, &item.Kind,
				&item.CreatedAt, &item.UpdatedAt)
		if database.IsNotFound(err) {
			return ErrNotFound
		}
		return err
	})
	return item, err
}

func (s *Store) Create(ctx context.Context, orgID, projectID, name, slug, kind string) (Environment, error) {
	item := Environment{ID: idgen.New("env"), OrgID: orgID, ProjectID: projectID, Name: name,
		Slug: slug, Kind: kind}
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM control.projects WHERE org_id = $1 AND id = $2)`,
			orgID, projectID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		return tx.QueryRow(ctx, `
			INSERT INTO control.environments (id, org_id, project_id, name, slug, kind)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING created_at, updated_at`,
			item.ID, orgID, projectID, name, slug, kind).Scan(&item.CreatedAt, &item.UpdatedAt)
	})
	return item, err
}

func ValidKind(kind string) bool {
	return kind == "development" || kind == "staging" || kind == "production"
}

func NormalizeSlug(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
