package audit

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
)

// Middleware records successful and failed mutating organisation-scoped requests. It is mounted after
// ResolveMembership, so the tenant and actor are already trusted.
func Middleware(db *database.DB, log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(recorder, r)
			membership, ok := auth.MembershipFrom(r.Context())
			if !ok {
				return
			}
			action, resourceType := deriveAction(r)
			outcome := "success"
			if recorder.status == http.StatusUnauthorized || recorder.status == http.StatusForbidden {
				outcome = "denied"
			} else if recorder.status >= 400 {
				outcome = "failure"
			}
			event := Event{
				OrgID: membership.OrgID, ProjectID: chi.URLParam(r, "project_id"), Action: action,
				ResourceType: resourceType, ResourceID: resourceID(r), Outcome: outcome,
				Details: map[string]any{"method": r.Method, "path": r.URL.Path, "status": recorder.status},
			}
			if err := Record(r.Context(), db, event); err != nil {
				log.Warn("audit append failed", zap.String("action", action), zap.Error(err))
			}
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	return r.ResponseWriter.Write(data)
}

func deriveAction(r *http.Request) (string, string) {
	path := r.URL.Path
	if index := strings.LastIndex(path, ":"); index >= 0 {
		suffix := strings.Trim(path[index+1:], "/")
		switch {
		case strings.Contains(path, "/secrets/"):
			return "secret." + suffix, "secret"
		case strings.Contains(path, "/workflows/"):
			return "workflow." + suffix, "workflow"
		case strings.Contains(path, "/agents/"):
			return "agent." + suffix, "agent"
		case strings.Contains(path, "/executions/"):
			if suffix == "approve" || suffix == "reject" || suffix == "signal" {
				return "task." + suffix, "task"
			}
			return "execution." + suffix, "execution"
		default:
			return "admin." + suffix, "admin"
		}
	}
	switch {
	case strings.Contains(path, "/secrets"):
		return writeAction(r.Method, "secret"), "secret"
	case strings.Contains(path, "/api-keys"):
		return writeAction(r.Method, "api_key"), "api_key"
	case strings.Contains(path, "/members"):
		return writeAction(r.Method, "member"), "member"
	case strings.Contains(path, "/agents/") && strings.Contains(path, "/versions"):
		return "agent.version.create", "agent_version"
	case strings.Contains(path, "/agents"):
		return writeAction(r.Method, "agent"), "agent"
	case strings.Contains(path, "/workflows/") && strings.Contains(path, "/versions"):
		return "workflow.version.create", "workflow_version"
	case strings.Contains(path, "/workflows"):
		return writeAction(r.Method, "workflow"), "workflow"
	case strings.Contains(path, "/executions"):
		return writeAction(r.Method, "execution"), "execution"
	case strings.Contains(path, "/environments"):
		return writeAction(r.Method, "environment"), "environment"
	case strings.Contains(path, "/projects"):
		return writeAction(r.Method, "project"), "project"
	default:
		return "admin." + strings.ToLower(r.Method), "admin"
	}
}

func writeAction(method, resource string) string {
	switch method {
	case http.MethodPost:
		return resource + ".create"
	case http.MethodPatch:
		return resource + ".update"
	case http.MethodDelete:
		return resource + ".revoke"
	default:
		return resource + "." + strings.ToLower(method)
	}
}

func resourceID(r *http.Request) string {
	for _, key := range []string{
		"secret_id", "key_id", "principal_id", "execution_id", "task_id", "workflow_id",
		"agent_id", "project_id", "environment_id",
	} {
		if value := chi.URLParam(r, key); value != "" {
			return value
		}
	}
	return ""
}
