// Package projects implements project endpoints.
package projects

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
	"github.com/agent-platform/agent-platform/services/api/internal/pagination"
	"github.com/agent-platform/agent-platform/services/api/internal/respond"
)

var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

type Handlers struct {
	store *Store
}

func NewHandlers(store *Store) *Handlers {
	return &Handlers{store: store}
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	limit := pagination.Limit(r, 50, 100)
	items, nextCursor, err := h.store.List(r.Context(), membership.OrgID, limit, r.URL.Query().Get("cursor"))
	if err != nil {
		if err.Error() == "invalid cursor" {
			respond.Error(w, r, http.StatusUnprocessableEntity, "invalid_cursor", "cursor is invalid")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list projects")
		return
	}
	if scope := auth.ProjectScope(r.Context()); scope != "" {
		project, getErr := h.store.Get(r.Context(), membership.OrgID, scope)
		if getErr == nil {
			items = []Project{project}
		} else {
			items = []Project{}
		}
		nextCursor = ""
	}
	response := map[string]any{"items": items}
	if nextCursor != "" {
		response["next_cursor"] = nextCursor
	} else {
		response["next_cursor"] = nil
	}
	respond.JSON(w, http.StatusOK, response)
}

type createRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	if auth.ProjectScope(r.Context()) != "" {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "project-scoped API keys cannot create projects")
		return
	}
	if err := auth.RequireRole(r.Context(), auth.RoleOperator); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "operator role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	var request createRequest
	if err := respond.Decode(w, r, 32<<10, &request); err != nil {
		respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" || len(request.Name) > 120 {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "name must be 1..120 characters")
		return
	}
	if !slugPattern.MatchString(request.Slug) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "slug must match ^[a-z][a-z0-9-]{1,62}$")
		return
	}
	project, err := h.store.Create(r.Context(), membership.OrgID, request.Name, request.Slug)
	if err != nil {
		if database.IsUniqueViolation(err) {
			respond.Error(w, r, http.StatusConflict, "conflict", "project slug is already in use")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not create project")
		return
	}
	respond.JSON(w, http.StatusCreated, project)
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	project, err := h.store.Get(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"))
	if errors.Is(err, ErrNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not load project")
		return
	}
	respond.JSON(w, http.StatusOK, project)
}
