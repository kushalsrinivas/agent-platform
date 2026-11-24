package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// APIKeyVerifier resolves `apk_…` credentials through the pre-tenant SECURITY DEFINER function and delegates all
// other tokens to the configured OIDC/dev verifier.
type APIKeyVerifier struct {
	pool     *pgxpool.Pool
	fallback Verifier
}

func NewAPIKeyVerifier(pool *pgxpool.Pool, fallback Verifier) *APIKeyVerifier {
	return &APIKeyVerifier{pool: pool, fallback: fallback}
}

func (v *APIKeyVerifier) IsDevelopment() bool { return v.fallback.IsDevelopment() }

func (v *APIKeyVerifier) Verify(ctx context.Context, token string) (Principal, error) {
	if !strings.HasPrefix(token, "apk_") {
		return v.fallback.Verify(ctx, token)
	}
	sum := sha256.Sum256([]byte(token))
	var keyID, orgID, role, principalID string
	var projectID pgtype.Text
	if err := v.pool.QueryRow(ctx, `
		SELECT key_id, org_id, project_id, role, principal_id
		FROM control.resolve_api_key($1)`, hex.EncodeToString(sum[:])).
		Scan(&keyID, &orgID, &projectID, &role, &principalID); err != nil {
		return Principal{}, ErrUnauthenticated
	}
	_ = keyID
	principal := Principal{ID: principalID, APIKeyOrgID: orgID, APIKeyRole: Role(role)}
	if projectID.Valid {
		principal.APIKeyProjectID = projectID.String
	}
	return principal, nil
}
