// Package workflows implements workflow definition, versioning and publishing endpoints.
package workflows

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/agent-platform/agent-platform/packages/contracts"
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

type versionRequest struct {
	Graph contracts.WorkflowGraph `json:"graph"`
}

type createWorkflowRequest struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Version     versionRequest `json:"version"`
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	limit := pagination.Limit(r, 50, 100)
	items, nextCursor, err := h.store.List(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		limit, r.URL.Query().Get("cursor"))
	if err != nil {
		if err.Error() == "invalid cursor" {
			respond.Error(w, r, http.StatusUnprocessableEntity, "invalid_cursor", "cursor is invalid")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list workflows")
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

	var request createWorkflowRequest
	if err := respond.Decode(w, r, 1<<20, &request); err != nil {
		if respond.IsBodyTooLarge(err) {
			respond.Error(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds 1 MiB")
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
	if err := request.Version.Graph.Validate(); err != nil {
		respond.ErrorWithDetails(w, r, http.StatusUnprocessableEntity, "invalid_graph", "workflow graph is invalid", map[string]any{"problems": err.Error()})
		return
	}
	workflow, err := h.store.Create(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		request.Name, request.Description, principal.ID, request.Version.Graph)
	if err != nil {
		h.respondStoreError(w, r, err, "could not create workflow")
		return
	}
	respond.JSON(w, http.StatusCreated, workflow)
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	workflow, err := h.store.Get(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "workflow_id"))
	if errors.Is(err, ErrNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "workflow not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not load workflow")
		return
	}
	respond.JSON(w, http.StatusOK, workflow)
}

func (h *Handlers) ListVersions(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	items, err := h.store.ListVersions(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "workflow_id"), pagination.Limit(r, 50, 100))
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list workflow versions")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *Handlers) GetVersion(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || version < 1 {
		respond.Error(w, r, http.StatusNotFound, "not_found", "workflow version not found")
		return
	}
	result, err := h.store.GetVersion(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "workflow_id"), version)
	if errors.Is(err, ErrVersionNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "workflow version not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not load workflow version")
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

	var request versionRequest
	if err := respond.Decode(w, r, 1<<20, &request); err != nil {
		if respond.IsBodyTooLarge(err) {
			respond.Error(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds 1 MiB")
			return
		}
		respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := request.Graph.Validate(); err != nil {
		respond.ErrorWithDetails(w, r, http.StatusUnprocessableEntity, "invalid_graph", "workflow graph is invalid", map[string]any{"problems": err.Error()})
		return
	}
	version, err := h.store.CreateVersion(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "workflow_id"), principal.ID, request.Graph)
	if err != nil {
		h.respondStoreError(w, r, err, "could not create workflow version")
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
		respond.Error(w, r, http.StatusNotFound, "not_found", "workflow version not found")
		return
	}
	result, err := h.store.Publish(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "workflow_id"), version)
	if err != nil {
		h.respondStoreError(w, r, err, "could not publish workflow version")
		return
	}
	respond.JSON(w, http.StatusOK, result)
}

func (h *Handlers) respondStoreError(w http.ResponseWriter, r *http.Request, err error, message string) {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrVersionNotFound):
		respond.Error(w, r, http.StatusNotFound, "not_found", "workflow or version not found")
	case errors.Is(err, ErrVersionState):
		respond.Error(w, r, http.StatusConflict, "conflict", "a deprecated version cannot be published")
	case errors.Is(err, ErrAgentUnavailable):
		respond.Error(w, r, http.StatusUnprocessableEntity, "agent_unavailable",
			"a referenced agent version is missing or not published")
	case database.IsUniqueViolation(err):
		respond.Error(w, r, http.StatusConflict, "conflict", "an identical workflow version already exists")
	case database.IsForeignKeyViolation(err):
		respond.Error(w, r, http.StatusNotFound, "not_found", "project not found")
	default:
		var validation *contracts.ValidationError
		if errors.As(err, &validation) {
			respond.ErrorWithDetails(w, r, http.StatusUnprocessableEntity, "invalid_graph",
				"workflow graph is invalid", map[string]any{"problems": validation.Error()})
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, "internal", message)
	}
}
