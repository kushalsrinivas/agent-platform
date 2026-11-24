// Package apikeys implements developer API key lifecycle.
package apikeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
)

var ErrNotFound = errors.New("api key not found")

type APIKey struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"org_id"`
	ProjectID  *string    `json:"project_id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	Role       string     `json:"role"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

type Created struct {
	APIKey
	Key string `json:"key"`
}

type Store struct {
	db *database.DB
}

func NewStore(db *database.DB) *Store { return &Store{db: db} }

func generate() (plaintext, prefix, hash string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", "", err
	}
	plaintext = "apk_" + base64.RawURLEncoding.EncodeToString(raw)
	prefix = plaintext[:12]
	sum := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(sum[:])
	return plaintext, prefix, hash, nil
}

func (s *Store) Create(ctx context.Context, orgID, projectID, name, role, createdBy string,
	expiresAt *time.Time) (Created, error) {
	plaintext, prefix, hash, err := generate()
	if err != nil {
		return Created{}, err
	}
	key := APIKey{
		ID: idgen.New("key"), OrgID: orgID, Name: name, KeyPrefix: prefix, Role: role,
		CreatedBy: createdBy, ExpiresAt: expiresAt,
	}
	if strings.TrimSpace(projectID) != "" {
		key.ProjectID = &projectID
	}
	err = s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		if key.ProjectID != nil {
			var exists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM control.projects WHERE org_id = $1 AND id = $2)`,
				orgID, projectID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO control.api_keys
				(id, org_id, project_id, name, key_prefix, key_hash, role, created_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING created_at`,
			key.ID, orgID, key.ProjectID, key.Name, key.KeyPrefix, hash, key.Role, createdBy, expiresAt).
			Scan(&key.CreatedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO control.memberships (org_id, principal_id, role)
			VALUES ($1, $2, $3)`, orgID, "apikey_"+key.ID, role)
		return err
	})
	if err != nil {
		return Created{}, err
	}
	return Created{APIKey: key, Key: plaintext}, nil
}

func (s *Store) List(ctx context.Context, orgID string, limit int) ([]APIKey, error) {
	keys := make([]APIKey, 0, limit)
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, org_id, project_id, name, key_prefix, role, created_by, created_at,
			       last_used_at, expires_at, revoked_at
			FROM control.api_keys
			WHERE org_id = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2`, orgID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var key APIKey
			var projectID pgtype.Text
			if err := rows.Scan(&key.ID, &key.OrgID, &projectID, &key.Name, &key.KeyPrefix,
				&key.Role, &key.CreatedBy, &key.CreatedAt, &key.LastUsedAt, &key.ExpiresAt,
				&key.RevokedAt); err != nil {
				return err
			}
			if projectID.Valid {
				key.ProjectID = &projectID.String
			}
			keys = append(keys, key)
		}
		return rows.Err()
	})
	return keys, err
}

func (s *Store) Revoke(ctx context.Context, orgID, keyID string) (APIKey, error) {
	var key APIKey
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		var projectID pgtype.Text
		err := tx.QueryRow(ctx, `
			UPDATE control.api_keys
			SET revoked_at = COALESCE(revoked_at, now())
			WHERE org_id = $1 AND id = $2
			RETURNING id, org_id, project_id, name, key_prefix, role, created_by, created_at,
			          last_used_at, expires_at, revoked_at`, orgID, keyID).
			Scan(&key.ID, &key.OrgID, &projectID, &key.Name, &key.KeyPrefix, &key.Role,
				&key.CreatedBy, &key.CreatedAt, &key.LastUsedAt, &key.ExpiresAt, &key.RevokedAt)
		if projectID.Valid {
			key.ProjectID = &projectID.String
		}
		if database.IsNotFound(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			DELETE FROM control.memberships WHERE org_id = $1 AND principal_id = $2`,
			orgID, "apikey_"+key.ID)
		return err
	})
	return key, err
}

func ValidRole(role string) bool { return auth.ValidRole(role) }
