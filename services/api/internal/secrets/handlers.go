// Package secrets manages encrypted project-scoped credentials.
package secrets

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/agent-platform/agent-platform/services/api/internal/audit"
	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
	"github.com/agent-platform/agent-platform/services/api/internal/respond"
)

var secretNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,119}$`)

type Handlers struct {
	store *Store
	db    *database.DB
}

func NewHandlers(store *Store, db *database.DB) *Handlers { return &Handlers{store: store, db: db} }

func (h *Handlers) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if err := auth.RequireRole(r.Context(), auth.RoleAdmin); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "admin role required")
		return false
	}
	return true
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if h.store.keyring == nil {
		respond.Error(w, r, http.StatusServiceUnavailable, "secrets_unavailable", "secret store is not configured")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	items, err := h.store.List(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		r.URL.Query().Get("environment_id"))
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list secrets")
		return
	}
	visible := make([]Secret, 0, len(items))
	for _, item := range items {
		if item.RevokedAt == nil {
			visible = append(visible, item)
		}
	}
	_ = audit.Record(r.Context(), h.db, audit.Event{
		ProjectID: chi.URLParam(r, "project_id"), Action: "secret.list", ResourceType: "secret",
		Details: map[string]any{"count": len(visible)},
	})
	respond.JSON(w, http.StatusOK, map[string]any{"items": visible, "next_cursor": nil})
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	item, err := h.store.Get(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "secret_id"))
	if errors.Is(err, ErrNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "secret not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not load secret")
		return
	}
	_ = audit.Record(r.Context(), h.db, audit.Event{
		ProjectID: item.ProjectID, Action: "secret.read_metadata", ResourceType: "secret", ResourceID: item.ID,
	})
	respond.JSON(w, http.StatusOK, item)
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	principal, _ := auth.PrincipalFrom(r.Context())
	membership, _ := auth.MembershipFrom(r.Context())
	var request struct {
		Name          string `json:"name"`
		Value         string `json:"value"`
		EnvironmentID string `json:"environment_id,omitempty"`
		Description   string `json:"description,omitempty"`
	}
	if err := respond.Decode(w, r, 128<<10, &request); err != nil {
		if respond.IsBodyTooLarge(err) {
			respond.Error(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds 128 KiB")
			return
		}
		respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	if !secretNamePattern.MatchString(request.Name) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed",
			"name must match ^[A-Za-z][A-Za-z0-9_.-]{0,119}$")
		return
	}
	if len(request.Description) > 2000 {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "description must be at most 2000 characters")
		return
	}
	if len(request.Value) == 0 || len(request.Value) > 64<<10 {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "value must be 1 byte..64 KiB")
		return
	}
	item, err := h.store.Create(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		strings.TrimSpace(request.EnvironmentID), request.Name, request.Description, request.Value, principal.ID)
	if errors.Is(err, ErrUnavailable) {
		respond.Error(w, r, http.StatusServiceUnavailable, "secrets_unavailable", "secret store is not configured")
		return
	}
	if errors.Is(err, ErrNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "environment not found")
		return
	}
	if database.IsUniqueViolation(err) {
		respond.Error(w, r, http.StatusConflict, "conflict", "a secret with that name already exists in this scope")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not create secret")
		return
	}
	respond.JSON(w, http.StatusCreated, item)
}

func (h *Handlers) Rotate(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	var request struct {
		Value string `json:"value"`
	}
	if err := respond.Decode(w, r, 128<<10, &request); err != nil {
		respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(request.Value) > 64<<10 {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "value must be at most 64 KiB")
		return
	}
	item, err := h.store.Rotate(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "secret_id"), request.Value)
	if errors.Is(err, ErrNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "secret not found")
		return
	}
	if errors.Is(err, ErrUnavailable) {
		respond.Error(w, r, http.StatusServiceUnavailable, "secrets_unavailable", "secret store is not configured")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not rotate secret")
		return
	}
	respond.JSON(w, http.StatusOK, item)
}

func (h *Handlers) Revoke(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	_, err := h.store.Revoke(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "secret_id"))
	if errors.Is(err, ErrNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "secret not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not revoke secret")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
