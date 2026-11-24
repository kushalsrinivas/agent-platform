package developer

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/pagination"
	"github.com/agent-platform/agent-platform/services/api/internal/respond"
)

type Handlers struct {
	store *Store
}

func NewHandlers(store *Store) *Handlers { return &Handlers{store: store} }

func (h *Handlers) Workers(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && status != "starting" && status != "available" && status != "busy" &&
		status != "draining" && status != "unhealthy" && status != "terminated" {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "invalid worker status")
		return
	}
	items, err := h.store.Workers(r.Context(), r.URL.Query().Get("pool"), status,
		pagination.Limit(r, 50, 200))
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list workers")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *Handlers) Tools(w http.ResponseWriter, _ *http.Request) {
	respond.JSON(w, http.StatusOK, map[string]any{"items": BuiltinTools(), "next_cursor": nil})
}

func (h *Handlers) MCPServers(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	items, err := h.store.MCPServers(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"))
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list mcp servers")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *Handlers) Usage(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	projectID := r.URL.Query().Get("project_id")
	if scope := auth.ProjectScope(r.Context()); scope != "" {
		projectID = scope
	}
	to := time.Now().UTC()
	from := to.Add(-30 * 24 * time.Hour)
	if raw := r.URL.Query().Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "to must be RFC3339")
			return
		}
		to = parsed.UTC()
	}
	if raw := r.URL.Query().Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "from must be RFC3339")
			return
		}
		from = parsed.UTC()
	}
	if !from.Before(to) || to.Sub(from) > 366*24*time.Hour {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed",
			"from must be before to and the window must be at most 366 days")
		return
	}
	usage, err := h.store.Usage(r.Context(), membership.OrgID, projectID, from, to)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not load usage")
		return
	}
	respond.JSON(w, http.StatusOK, usage)
}

func (h *Handlers) Approvals(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	projectID := r.URL.Query().Get("project_id")
	if scope := auth.ProjectScope(r.Context()); scope != "" {
		projectID = scope
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "pending" && status != "approved" && status != "rejected" && status != "expired" {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "invalid approval status")
		return
	}
	items, err := h.store.Approvals(r.Context(), membership.OrgID, projectID,
		status, pagination.Limit(r, 50, 200))
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list approvals")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}
