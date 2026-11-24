// Package auth authenticates requests and carries the principal and resolved tenant role.
//
// Phase 1 delegates registration, login, password hashing and session management to the OIDC identity provider.
// The API verifies bearer JWTs. A development-only token issuer exists so local work and tests need no IdP; it is
// never mounted when AUTH_MODE=oidc.
package auth

import (
	"context"
	"errors"
)

type Role string

const (
	RoleViewer    Role = "viewer"
	RoleOperator  Role = "operator" // legacy name, same permissions as developer
	RoleDeveloper Role = "developer"
	RoleAdmin     Role = "admin"
	RoleOwner     Role = "owner"
)

func (r Role) Rank() int {
	switch r {
	case RoleViewer:
		return 1
	case RoleOperator, RoleDeveloper:
		return 2
	case RoleAdmin:
		return 3
	case RoleOwner:
		return 4
	default:
		return 0
	}
}

func ValidRole(role string) bool {
	return role == string(RoleViewer) || role == string(RoleOperator) ||
		role == string(RoleDeveloper) || role == string(RoleAdmin) || role == string(RoleOwner)
}

// CanManageSecrets reports whether the role may create, rotate or revoke project credentials.
func CanManageSecrets(role Role) bool { return role.Rank() >= RoleAdmin.Rank() }

// CanManageMembers reports whether the role may add, update or remove members.
func CanManageMembers(role Role) bool { return role.Rank() >= RoleAdmin.Rank() }

var (
	ErrUnauthenticated = errors.New("authentication required")
	ErrForbidden       = errors.New("insufficient role")
)

// Principal is the authenticated identity. API-key requests carry their locked organisation/project scope and
// role so they never need a membership lookup before tenant context exists.
type Principal struct {
	ID              string
	APIKeyOrgID     string
	APIKeyProjectID string
	APIKeyRole      Role
}

type contextKey int

const (
	principalKey contextKey = iota
	membershipKey
)

// Membership is the caller's role in the route's organisation.
type Membership struct {
	OrgID string
	Role  Role
}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey, principal)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey).(Principal)
	return principal, ok && principal.ID != ""
}

func WithMembership(ctx context.Context, membership Membership) context.Context {
	return context.WithValue(ctx, membershipKey, membership)
}

func MembershipFrom(ctx context.Context) (Membership, bool) {
	membership, ok := ctx.Value(membershipKey).(Membership)
	return membership, ok
}

// RequireRole checks the role resolved by the organization route middleware.
// ProjectScope returns the project locked to an API key, or "" for user/org credentials.
func ProjectScope(ctx context.Context) string {
	principal, ok := PrincipalFrom(ctx)
	if !ok {
		return ""
	}
	return principal.APIKeyProjectID
}

func RequireRole(ctx context.Context, minimum Role) error {
	membership, ok := MembershipFrom(ctx)
	if !ok || membership.Role.Rank() < minimum.Rank() {
		return ErrForbidden
	}
	return nil
}
