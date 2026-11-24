package environments

import (
	"errors"
	"net/http"
	"strings"

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
	membership, _ := auth.MembershipFrom(r.Context())
	items, err := h.store.List(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"))
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list environments")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	item, err := h.store.Get(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "environment_id"))
	if errors.Is(err, ErrNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "environment not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not load environment")
		return
	}
	respond.JSON(w, http.StatusOK, item)
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	if err := auth.RequireRole(r.Context(), auth.RoleOperator); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "operator role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	var request struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
		Kind string `json:"kind"`
	}
	if err := respond.Decode(w, r, 16<<10, &request); err != nil {
		respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	request.Slug = NormalizeSlug(request.Slug)
	if request.Slug == "" {
		request.Slug = slugify(request.Name)
	}
	if request.Kind == "" {
		request.Kind = "development"
	}
	if request.Name == "" || len(request.Name) > 120 {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "name must be 1..120 characters")
		return
	}
	if !validSlug(request.Slug) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed",
			"slug must be 2..63 lowercase letters, digits or hyphens and start with a letter")
		return
	}
	if !ValidKind(request.Kind) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed",
			"kind must be development, staging or production")
		return
	}
	item, err := h.store.Create(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		request.Name, request.Slug, request.Kind)
	if errors.Is(err, ErrNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if database.IsUniqueViolation(err) {
		respond.Error(w, r, http.StatusConflict, "conflict", "an environment with this slug already exists")
		return
	}
	if database.IsForeignKeyViolation(err) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not create environment")
		return
	}
	respond.JSON(w, http.StatusCreated, item)
}

func slugify(name string) string {
	var builder strings.Builder
	lastDash := false
	for _, char := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			builder.WriteRune(char)
			lastDash = false
		default:
			if !lastDash && builder.Len() > 0 {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		slug = "environment"
	}
	if slug[0] < 'a' || slug[0] > 'z' || len(slug) < 2 {
		slug = "env-" + slug
	}
	if len(slug) > 63 {
		slug = strings.Trim(slug[:63], "-")
	}
	return slug
}

func validSlug(slug string) bool {
	if len(slug) < 2 || len(slug) > 63 || slug[0] < 'a' || slug[0] > 'z' {
		return false
	}
	for index := 1; index < len(slug); index++ {
		ch := slug[index]
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
			return false
		}
	}
	return true
}
