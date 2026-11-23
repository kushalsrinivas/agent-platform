// Package organizations implements organisation, membership and tenancy endpoints.
package organizations

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
	"github.com/agent-platform/agent-platform/services/api/internal/respond"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type Handlers struct {
	store *Store
}

func NewHandlers(store *Store) *Handlers {
	return &Handlers{store: store}
}

// ResolveMembership loads the caller's role once per organisation route.
func (h *Handlers) ResolveMembership(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFrom(r.Context())
		if !ok {
			respond.Error(w, r, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		orgID := chi.URLParam(r, "org_id")
		role, err := h.store.Membership(r.Context(), orgID, principal.ID)
		if errors.Is(err, ErrNotFound) {
			// Cross-tenant IDs are indistinguishable from missing IDs.
			respond.Error(w, r, http.StatusNotFound, "not_found", "organization not found")
			return
		}
		if err != nil {
			respond.Error(w, r, http.StatusInternalServerError, "internal", "could not resolve membership")
			return
		}
		ctx := auth.WithMembership(r.Context(), auth.Membership{OrgID: orgID, Role: role})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFrom(r.Context())
	items, err := h.store.ListForPrincipal(r.Context(), principal.ID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list organizations")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items})
}

type createOrganizationRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFrom(r.Context())
	var request createOrganizationRequest
	if err := respond.Decode(w, r, 32<<10, &request); err != nil {
		respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" || len(request.Name) > 200 {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "name must be 1..200 characters")
		return
	}
	if !slugPattern.MatchString(request.Slug) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "slug must match ^[a-z0-9][a-z0-9-]{0,62}$")
		return
	}

	org, err := h.store.Create(r.Context(), request.Name, request.Slug, principal.ID)
	if err != nil {
		if database.IsUniqueViolation(err) {
			respond.Error(w, r, http.StatusConflict, "conflict", "organization slug is already in use")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not create organization")
		return
	}
	respond.JSON(w, http.StatusCreated, org)
}

func (h *Handlers) ListMembers(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	items, err := h.store.ListMembers(r.Context(), membership.OrgID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list members")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items})
}

type memberRequest struct {
	PrincipalID string `json:"principal_id,omitempty"`
	Role        string `json:"role"`
}

func (h *Handlers) AddMember(w http.ResponseWriter, r *http.Request) {
	if err := auth.RequireRole(r.Context(), auth.RoleOwner); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "owner role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	var request memberRequest
	if err := respond.Decode(w, r, 32<<10, &request); err != nil {
		respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.PrincipalID = strings.TrimSpace(request.PrincipalID)
	if request.PrincipalID == "" || len(request.PrincipalID) > 255 {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "principal_id must be 1..255 characters")
		return
	}
	if !auth.ValidRole(request.Role) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "role must be owner, operator or viewer")
		return
	}
	member, err := h.store.AddMember(r.Context(), membership.OrgID, request.PrincipalID, request.Role)
	if err != nil {
		if database.IsUniqueViolation(err) {
			respond.Error(w, r, http.StatusConflict, "conflict", "principal is already a member")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not add member")
		return
	}
	respond.JSON(w, http.StatusCreated, member)
}

func (h *Handlers) UpdateMember(w http.ResponseWriter, r *http.Request) {
	if err := auth.RequireRole(r.Context(), auth.RoleOwner); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "owner role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	var request memberRequest
	if err := respond.Decode(w, r, 32<<10, &request); err != nil {
		respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !auth.ValidRole(request.Role) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "role must be owner, operator or viewer")
		return
	}
	principalID := chi.URLParam(r, "principal_id")
	member, err := h.store.UpdateMember(r.Context(), membership.OrgID, principalID, request.Role)
	if errors.Is(err, ErrLastOwner) {
		respond.Error(w, r, http.StatusConflict, "last_owner", "the organization must keep at least one owner")
		return
	}
	if database.IsNotFound(err) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "member not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not update member")
		return
	}
	respond.JSON(w, http.StatusOK, member)
}

func (h *Handlers) RemoveMember(w http.ResponseWriter, r *http.Request) {
	if err := auth.RequireRole(r.Context(), auth.RoleOwner); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "owner role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	principalID := chi.URLParam(r, "principal_id")
	err := h.store.RemoveMember(r.Context(), membership.OrgID, principalID)
	if errors.Is(err, ErrLastOwner) {
		respond.Error(w, r, http.StatusConflict, "last_owner", "the organization must keep at least one owner")
		return
	}
	if database.IsNotFound(err) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "member not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not remove member")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
