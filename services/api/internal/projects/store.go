package projects

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
)

var ErrNotFound = errors.New("project not found")

type Project struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store struct {
	db *database.DB
}

func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

func (s *Store) List(ctx context.Context, orgID string, limit int, cursor string) ([]Project, string, error) {
	projects := make([]Project, 0, limit)
	var nextCursor string
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		query := `
			SELECT id, org_id, name, slug, created_at, updated_at
			FROM control.projects
			WHERE org_id = $1`
		args := []any{orgID}

		if cursor != "" {
			parts, err := database.DecodeCursor(cursor)
			if err != nil || len(parts) != 2 {
				return errors.New("invalid cursor")
			}
			query += ` AND (created_at, id) < ($2::timestamptz, $3)`
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
			var project Project
			if err := rows.Scan(&project.ID, &project.OrgID, &project.Name, &project.Slug,
				&project.CreatedAt, &project.UpdatedAt); err != nil {
				return err
			}
			projects = append(projects, project)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(projects) > limit {
			projects = projects[:limit]
			last := projects[limit-1]
			nextCursor = database.EncodeCursor(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID)
		}
		return nil
	})
	return projects, nextCursor, err
}

func (s *Store) Get(ctx context.Context, orgID, projectID string) (Project, error) {
	var project Project
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, org_id, name, slug, created_at, updated_at
			FROM control.projects
			WHERE org_id = $1 AND id = $2`, orgID, projectID).
			Scan(&project.ID, &project.OrgID, &project.Name, &project.Slug, &project.CreatedAt, &project.UpdatedAt)
	})
	if database.IsNotFound(err) {
		return Project{}, ErrNotFound
	}
	return project, err
}

func (s *Store) Create(ctx context.Context, orgID, name, slug string) (Project, error) {
	project := Project{ID: idgen.New("proj"), OrgID: orgID, Name: name, Slug: slug}
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO control.projects (id, org_id, name, slug)
			VALUES ($1, $2, $3, $4)
			RETURNING created_at, updated_at`, project.ID, orgID, name, slug).
			Scan(&project.CreatedAt, &project.UpdatedAt)
	})
	return project, err
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
