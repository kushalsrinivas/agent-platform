package apikeys

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
	"github.com/agent-platform/agent-platform/services/api/internal/respond"
)

type Handlers struct {
	store *Store
}

func NewHandlers(store *Store) *Handlers { return &Handlers{store: store} }

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	if auth.ProjectScope(r.Context()) != "" {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "project-scoped api keys cannot manage api keys")
		return
	}
	if err := auth.RequireRole(r.Context(), auth.RoleAdmin); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "admin role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	keys, err := h.store.List(r.Context(), membership.OrgID, 100)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list api keys")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": keys, "next_cursor": nil})
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	if auth.ProjectScope(r.Context()) != "" {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "project-scoped api keys cannot manage api keys")
		return
	}
	if err := auth.RequireRole(r.Context(), auth.RoleAdmin); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "admin role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	principal, _ := auth.PrincipalFrom(r.Context())
	var request struct {
		Name      string     `json:"name"`
		ProjectID string     `json:"project_id,omitempty"`
		Role      string     `json:"role,omitempty"`
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
	}
	if err := respond.Decode(w, r, 16<<10, &request); err != nil {
		respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" || len(request.Name) > 120 {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "name must be 1..120 characters")
		return
	}
	if request.Role == "" {
		request.Role = string(auth.RoleOperator)
	}
	if request.ProjectID != "" && request.Role == string(auth.RoleOwner) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed",
			"project-scoped API keys cannot have the owner role")
		return
	}
	if request.Role == string(auth.RoleOwner) && membership.Role != auth.RoleOwner {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "only an owner may create an owner API key")
		return
	}
	if !ValidRole(request.Role) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed",
			"role must be viewer, developer, operator, admin or owner")
		return
	}
	if request.ExpiresAt != nil && !request.ExpiresAt.After(time.Now()) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "expires_at must be in the future")
		return
	}
	key, err := h.store.Create(r.Context(), membership.OrgID, request.ProjectID, request.Name,
		request.Role, principal.ID, request.ExpiresAt)
	if errors.Is(err, ErrNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		if database.IsUniqueViolation(err) {
			respond.Error(w, r, http.StatusConflict, "conflict", "api key already exists")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not create api key")
		return
	}
	respond.JSON(w, http.StatusCreated, key)
}

func (h *Handlers) Revoke(w http.ResponseWriter, r *http.Request) {
	if auth.ProjectScope(r.Context()) != "" {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "project-scoped api keys cannot manage api keys")
		return
	}
	if err := auth.RequireRole(r.Context(), auth.RoleAdmin); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "admin role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	key, err := h.store.Revoke(r.Context(), membership.OrgID, chi.URLParam(r, "key_id"))
	if errors.Is(err, ErrNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "api key not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not revoke api key")
		return
	}
	respond.JSON(w, http.StatusOK, key)
}
