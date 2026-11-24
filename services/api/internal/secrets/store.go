package secrets

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/internal/platform/secretbox"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
)

var (
	ErrNotFound    = errors.New("secret not found")
	ErrUnavailable = errors.New("secret store is not configured")
)

type Secret struct {
	ID            string     `json:"id"`
	OrgID         string     `json:"org_id"`
	ProjectID     string     `json:"project_id"`
	EnvironmentID *string    `json:"environment_id"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Version       int        `json:"version"`
	KeyID         string     `json:"key_id"`
	CreatedBy     string     `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	RotatedAt     *time.Time `json:"rotated_at"`
	LastAccessed  *time.Time `json:"last_accessed_at"`
	RevokedAt     *time.Time `json:"revoked_at"`
}

type Store struct {
	db      *database.DB
	keyring *secretbox.Keyring
}

func NewStore(db *database.DB, keyring *secretbox.Keyring) *Store {
	return &Store{db: db, keyring: keyring}
}

func (s *Store) List(ctx context.Context, orgID, projectID, environmentID string) ([]Secret, error) {
	items := make([]Secret, 0)
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, org_id, project_id, environment_id, name, description, version, kms_key_id,
			       created_by, created_at, rotated_at, last_accessed_at, revoked_at
			FROM control.credentials
			WHERE org_id = $1 AND project_id = $2
			  AND ($3 = '' OR environment_id = $3)
			ORDER BY environment_id NULLS FIRST, name`, orgID, projectID, environmentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Secret
			if err := rows.Scan(&item.ID, &item.OrgID, &item.ProjectID, &item.EnvironmentID, &item.Name,
				&item.Description, &item.Version, &item.KeyID, &item.CreatedBy, &item.CreatedAt,
				&item.RotatedAt, &item.LastAccessed, &item.RevokedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (s *Store) Create(ctx context.Context, orgID, projectID, environmentID, name, description, value,
	principalID string) (Secret, error) {
	if s.keyring == nil {
		return Secret{}, ErrUnavailable
	}
	name = strings.TrimSpace(name)
	if len(value) == 0 || len(value) > 64<<10 {
		return Secret{}, errors.New("value must be between 1 byte and 64 KiB")
	}
	additional := secretbox.AdditionalData(orgID, projectID, environmentID, name)
	ciphertext, nonce, keyID, err := s.keyring.Encrypt([]byte(value), additional)
	if err != nil {
		return Secret{}, err
	}
	item := Secret{
		ID: idgen.New("cred"), OrgID: orgID, ProjectID: projectID, Name: name,
		Description: description, Version: 1, KeyID: keyID, CreatedBy: principalID,
	}
	if environmentID != "" {
		item.EnvironmentID = &environmentID
	}
	err = s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		if environmentID != "" {
			var exists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM control.environments
					WHERE org_id = $1 AND project_id = $2 AND id = $3)`,
				orgID, projectID, environmentID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
		}
		return tx.QueryRow(ctx, `
			INSERT INTO control.credentials
				(id, org_id, project_id, environment_id, name, description, ciphertext, nonce, kms_key_id,
				 version, created_by)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, 1, $10)
			RETURNING created_at`,
			item.ID, orgID, projectID, environmentID, name, description, ciphertext, nonce, keyID, principalID).
			Scan(&item.CreatedAt)
	})
	return item, err
}

func (s *Store) Get(ctx context.Context, orgID, projectID, secretID string) (Secret, error) {
	var item Secret
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, org_id, project_id, environment_id, name, description, version, kms_key_id,
			       created_by, created_at, rotated_at, last_accessed_at, revoked_at
			FROM control.credentials
			WHERE org_id = $1 AND project_id = $2 AND id = $3`, orgID, projectID, secretID).
			Scan(&item.ID, &item.OrgID, &item.ProjectID, &item.EnvironmentID, &item.Name, &item.Description,
				&item.Version, &item.KeyID, &item.CreatedBy, &item.CreatedAt, &item.RotatedAt,
				&item.LastAccessed, &item.RevokedAt)
	})
	if database.IsNotFound(err) {
		return Secret{}, ErrNotFound
	}
	return item, err
}

func (s *Store) Rotate(ctx context.Context, orgID, projectID, secretID, value string) (Secret, error) {
	if s.keyring == nil {
		return Secret{}, ErrUnavailable
	}
	var item Secret
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		var environmentID, name, keyID string
		var nonce, ciphertext []byte
		if err := tx.QueryRow(ctx, `
			SELECT id, org_id, project_id, COALESCE(environment_id, ''), name, description, version,
			       kms_key_id, ciphertext, nonce, created_by, created_at, rotated_at, last_accessed_at, revoked_at
			FROM control.credentials
			WHERE org_id = $1 AND project_id = $2 AND id = $3 AND revoked_at IS NULL
			FOR UPDATE`, orgID, projectID, secretID).
			Scan(&item.ID, &item.OrgID, &item.ProjectID, &environmentID, &name, &item.Description,
				&item.Version, &keyID, &ciphertext, &nonce, &item.CreatedBy, &item.CreatedAt,
				&item.RotatedAt, &item.LastAccessed, &item.RevokedAt); err != nil {
			return err
		}
		if environmentID != "" {
			item.EnvironmentID = &environmentID
		}
		item.Name, item.KeyID = name, keyID
		additional := secretbox.AdditionalData(orgID, projectID, environmentID, name)
		plaintext := []byte(value)
		var err error
		if len(plaintext) == 0 {
			plaintext, err = s.keyring.Decrypt(keyID, nonce, ciphertext, additional)
			if err != nil {
				return err
			}
		}
		nextCipher, nextNonce, nextKey, err := s.keyring.Encrypt(plaintext, additional)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			UPDATE control.credentials
			SET ciphertext = $1, nonce = $2, kms_key_id = $3, version = version + 1, rotated_at = now()
			WHERE org_id = $4 AND project_id = $5 AND id = $6
			RETURNING version, rotated_at`,
			nextCipher, nextNonce, nextKey, orgID, projectID, secretID).
			Scan(&item.Version, &item.RotatedAt)
	})
	if database.IsNotFound(err) {
		return Secret{}, ErrNotFound
	}
	if err != nil {
		return Secret{}, err
	}
	item.KeyID = s.keyring.ActiveKeyID()
	return item, nil
}

func (s *Store) Revoke(ctx context.Context, orgID, projectID, secretID string) (Secret, error) {
	var item Secret
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			UPDATE control.credentials SET revoked_at = now()
			WHERE org_id = $1 AND project_id = $2 AND id = $3 AND revoked_at IS NULL
			RETURNING id, org_id, project_id, environment_id, name, description, version, kms_key_id,
			          created_by, created_at, rotated_at, last_accessed_at, revoked_at`,
			orgID, projectID, secretID).
			Scan(&item.ID, &item.OrgID, &item.ProjectID, &item.EnvironmentID, &item.Name, &item.Description,
				&item.Version, &item.KeyID, &item.CreatedBy, &item.CreatedAt, &item.RotatedAt,
				&item.LastAccessed, &item.RevokedAt)
	})
	if database.IsNotFound(err) {
		return Secret{}, ErrNotFound
	}
	return item, err
}
