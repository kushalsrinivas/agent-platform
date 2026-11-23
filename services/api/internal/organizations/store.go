package organizations

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
)

var (
	ErrNotFound  = errors.New("organization not found")
	ErrLastOwner = errors.New("cannot remove or demote the last owner")
)

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

type Member struct {
	PrincipalID string    `json:"principal_id"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

type Store struct {
	db *database.DB
}

func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

// Membership resolves a principal's role before tenant context exists.
func (s *Store) Membership(ctx context.Context, orgID, principalID string) (auth.Role, error) {
	var role *string
	err := s.db.Pool().QueryRow(ctx,
		`SELECT control.authorize_membership($1, $2)`, orgID, principalID,
	).Scan(&role)
	if err != nil {
		if database.IsNotFound(err) {
			return "", ErrNotFound
		}
		return "", err
	}
	if role == nil {
		return "", ErrNotFound
	}
	return auth.Role(*role), nil
}

// ListForPrincipal returns organisations the caller belongs to. It uses the pre-tenant discovery function.
func (s *Store) ListForPrincipal(ctx context.Context, principalID string) ([]Organization, error) {
	organizations := make([]Organization, 0)
	err := s.db.WithPrincipal(ctx, principalID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT org_id, name, slug, created_at
			FROM control.list_orgs_for_principal()
			ORDER BY name, org_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var org Organization
			if err := rows.Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt); err != nil {
				return err
			}
			organizations = append(organizations, org)
		}
		return rows.Err()
	})
	return organizations, err
}

// Create inserts the organisation and makes the caller its first owner in one transaction.
func (s *Store) Create(ctx context.Context, name, slug, principalID string) (Organization, error) {
	org := Organization{ID: idgen.New("org"), Name: name, Slug: slug}
	err := s.db.WithTenant(ctx, org.ID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO control.organizations (id, name, slug)
			VALUES ($1, $2, $3)
			RETURNING created_at`, org.ID, org.Name, org.Slug).Scan(&org.CreatedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO control.memberships (org_id, principal_id, role)
			VALUES ($1, $2, 'owner')`, org.ID, principalID)
		return err
	})
	return org, err
}

func (s *Store) ListMembers(ctx context.Context, orgID string) ([]Member, error) {
	members := make([]Member, 0)
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT principal_id, role, created_at
			FROM control.memberships
			WHERE org_id = $1
			ORDER BY created_at, principal_id`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var member Member
			if err := rows.Scan(&member.PrincipalID, &member.Role, &member.CreatedAt); err != nil {
				return err
			}
			members = append(members, member)
		}
		return rows.Err()
	})
	return members, err
}

func (s *Store) AddMember(ctx context.Context, orgID, principalID, role string) (Member, error) {
	member := Member{PrincipalID: principalID, Role: role}
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, orgID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO control.memberships (org_id, principal_id, role)
			VALUES ($1, $2, $3)
			RETURNING created_at`, orgID, principalID, role).Scan(&member.CreatedAt)
	})
	return member, err
}

func (s *Store) UpdateMember(ctx context.Context, orgID, principalID, role string) (Member, error) {
	var member Member
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, orgID); err != nil {
			return err
		}
		var currentRole string
		if err := tx.QueryRow(ctx, `
			SELECT role FROM control.memberships
			WHERE org_id = $1 AND principal_id = $2`, orgID, principalID).Scan(&currentRole); err != nil {
			return err
		}
		if currentRole == string(auth.RoleOwner) && role != string(auth.RoleOwner) {
			owners, err := countOwners(ctx, tx, orgID)
			if err != nil {
				return err
			}
			if owners <= 1 {
				return ErrLastOwner
			}
		}
		member.PrincipalID = principalID
		member.Role = role
		return tx.QueryRow(ctx, `
			UPDATE control.memberships SET role = $3
			WHERE org_id = $1 AND principal_id = $2
			RETURNING created_at`, orgID, principalID, role).Scan(&member.CreatedAt)
	})
	return member, err
}

func (s *Store) RemoveMember(ctx context.Context, orgID, principalID string) error {
	return s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, orgID); err != nil {
			return err
		}
		var role string
		if err := tx.QueryRow(ctx, `
			SELECT role FROM control.memberships
			WHERE org_id = $1 AND principal_id = $2`, orgID, principalID).Scan(&role); err != nil {
			return err
		}
		if role == string(auth.RoleOwner) {
			owners, err := countOwners(ctx, tx, orgID)
			if err != nil {
				return err
			}
			if owners <= 1 {
				return ErrLastOwner
			}
		}
		_, err := tx.Exec(ctx, `
			DELETE FROM control.memberships
			WHERE org_id = $1 AND principal_id = $2`, orgID, principalID)
		return err
	})
}

func countOwners(ctx context.Context, tx pgx.Tx, orgID string) (int, error) {
	var owners int
	err := tx.QueryRow(ctx, `
		SELECT count(*) FROM control.memberships
		WHERE org_id = $1 AND role = 'owner'`, orgID).Scan(&owners)
	return owners, err
}
