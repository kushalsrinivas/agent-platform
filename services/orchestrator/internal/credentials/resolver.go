// Package credentials resolves project-scoped secret references for one fenced task attempt. Plaintext exists only
// in the response and is never logged or persisted.
package credentials

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/internal/platform/secretbox"
)

// Resolver decrypts project secrets for a fenced attempt.
type Resolver struct {
	pool    *pgxpool.Pool
	keyring *secretbox.Keyring
}

// New returns a resolver backed by the control schema and the active secret keyring.
func New(pool *pgxpool.Pool, keyring *secretbox.Keyring) *Resolver {
	return &Resolver{pool: pool, keyring: keyring}
}

// Resolve returns the named credentials for a project. Default-environment secrets win over environment-specific
// values only when environmentID matches; callers without an environment get only unscoped values.
func (r *Resolver) Resolve(ctx context.Context, orgID, projectID, environmentID string, names []string) (map[string]string, error) {
	values := map[string]string{}
	if r.keyring == nil || len(names) == 0 {
		return values, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, COALESCE(environment_id, ''), ciphertext, nonce, kms_key_id
		FROM control.credentials
		WHERE org_id = $1 AND project_id = $2 AND revoked_at IS NULL
		  AND name = ANY($3)
		  AND (COALESCE(environment_id, '') = '' OR COALESCE(environment_id, '') = $4)
		ORDER BY environment_id NULLS FIRST`, orgID, projectID, names, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0, len(names))
	for rows.Next() {
		var id, name, envID, keyID string
		var ciphertext, nonce []byte
		if err := rows.Scan(&id, &name, &envID, &ciphertext, &nonce, &keyID); err != nil {
			return nil, err
		}
		plaintext, err := r.keyring.Decrypt(keyID, nonce, ciphertext,
			secretbox.AdditionalData(orgID, projectID, envID, name))
		if err != nil {
			return nil, err
		}
		values[name] = string(plaintext)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		_, _ = r.pool.Exec(ctx, `
			UPDATE control.credentials SET last_accessed_at = now() WHERE id = ANY($1)`, ids)
		_, _ = r.pool.Exec(ctx, `
			INSERT INTO control.audit_events
				(id, org_id, project_id, actor_type, actor_id, action, resource_type, resource_id, outcome, details)
			VALUES ($1, $2, $3, 'worker', $4, 'secret.resolve', 'secret', '', 'success', $5)`,
			idgen.New("aud"), orgID, projectID, "worker", map[string]any{"names": names})
	}
	return values, nil
}

// Expiry is the lifetime advertised to the worker. The worker keeps values only in memory for the process.
func Expiry() time.Time { return time.Now().UTC().Add(15 * time.Minute) }
