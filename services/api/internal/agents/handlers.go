package agents

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
	"github.com/agent-platform/agent-platform/services/api/internal/pagination"
	"github.com/agent-platform/agent-platform/services/api/internal/respond"
)

type Handlers struct {
	store *Store
}

func NewHandlers(store *Store) *Handlers {
	return &Handlers{store: store}
}

type createAgentRequest struct {
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Version     createVersionRequest `json:"version"`
}

type createVersionRequest struct {
	Spec AgentSpec `json:"spec"`
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	projectID := chi.URLParam(r, "project_id")
	limit := pagination.Limit(r, 50, 100)
	items, nextCursor, err := h.store.List(r.Context(), membership.OrgID, projectID, limit, r.URL.Query().Get("cursor"))
	if err != nil {
		if err.Error() == "invalid cursor" {
			respond.Error(w, r, http.StatusUnprocessableEntity, "invalid_cursor", "cursor is invalid")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list agents")
		return
	}
	response := map[string]any{"items": items}
	if nextCursor != "" {
		response["next_cursor"] = nextCursor
	} else {
		response["next_cursor"] = nil
	}
	respond.JSON(w, http.StatusOK, response)
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	if err := auth.RequireRole(r.Context(), auth.RoleOperator); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "operator role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	principal, _ := auth.PrincipalFrom(r.Context())

	var request createAgentRequest
	if err := respond.Decode(w, r, 512<<10, &request); err != nil {
		if respond.IsBodyTooLarge(err) {
			respond.Error(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the limit")
			return
		}
		respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" || len(request.Name) > 120 {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "name must be 1..120 characters")
		return
	}
	if len(request.Description) > 2000 {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "description must be at most 2000 characters")
		return
	}
	if err := request.Version.Spec.Validate(); err != nil {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", err.Error())
		return
	}

	agent, err := h.store.Create(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		request.Name, request.Description, principal.ID, request.Version.Spec)
	if err != nil {
		if database.IsUniqueViolation(err) {
			respond.Error(w, r, http.StatusConflict, "conflict", "an agent with this name already exists in the project")
			return
		}
		if database.IsForeignKeyViolation(err) {
			respond.Error(w, r, http.StatusNotFound, "not_found", "project not found")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not create agent")
		return
	}
	respond.JSON(w, http.StatusCreated, agent)
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	agent, err := h.store.Get(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"), chi.URLParam(r, "agent_id"))
	if errors.Is(err, ErrNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not load agent")
		return
	}
	respond.JSON(w, http.StatusOK, agent)
}

func (h *Handlers) ListVersions(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	limit := pagination.Limit(r, 50, 100)
	items, err := h.store.ListVersions(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "agent_id"), limit, 0)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list agent versions")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *Handlers) GetVersion(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || version < 1 {
		respond.Error(w, r, http.StatusNotFound, "not_found", "agent version not found")
		return
	}
	result, err := h.store.GetVersion(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "agent_id"), version)
	if errors.Is(err, ErrVersionNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "agent version not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not load agent version")
		return
	}
	respond.JSON(w, http.StatusOK, result)
}

func (h *Handlers) CreateVersion(w http.ResponseWriter, r *http.Request) {
	if err := auth.RequireRole(r.Context(), auth.RoleOperator); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "operator role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	principal, _ := auth.PrincipalFrom(r.Context())

	var request createVersionRequest
	if err := respond.Decode(w, r, 512<<10, &request); err != nil {
		if respond.IsBodyTooLarge(err) {
			respond.Error(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the limit")
			return
		}
		respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := request.Spec.Validate(); err != nil {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", err.Error())
		return
	}
	version, err := h.store.CreateVersion(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "agent_id"), principal.ID, request.Spec)
	if errors.Is(err, ErrNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	if database.IsUniqueViolation(err) {
		respond.Error(w, r, http.StatusConflict, "conflict", "an identical agent version already exists")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not create agent version")
		return
	}
	respond.JSON(w, http.StatusCreated, version)
}

func (h *Handlers) PublishVersion(w http.ResponseWriter, r *http.Request) {
	if err := auth.RequireRole(r.Context(), auth.RoleOperator); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "operator role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || version < 1 {
		respond.Error(w, r, http.StatusNotFound, "not_found", "agent version not found")
		return
	}
	result, err := h.store.Publish(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "agent_id"), version)
	if errors.Is(err, ErrVersionNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "agent version not found")
		return
	}
	if errors.Is(err, ErrVersionState) {
		respond.Error(w, r, http.StatusConflict, "conflict", "a deprecated version cannot be published")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not publish agent version")
		return
	}
	respond.JSON(w, http.StatusOK, result)
}
