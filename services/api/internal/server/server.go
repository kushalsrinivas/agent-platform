// Package server mounts the public API routes.
package server

import (
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/services/api/internal/agents"
	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
	"github.com/agent-platform/agent-platform/services/api/internal/executions"
	"github.com/agent-platform/agent-platform/services/api/internal/middleware"
	"github.com/agent-platform/agent-platform/services/api/internal/organizations"
	"github.com/agent-platform/agent-platform/services/api/internal/projects"
	"github.com/agent-platform/agent-platform/services/api/internal/workflows"
)

// Config controls cross-cutting HTTP behaviour.
type Config struct {
	RateLimitPerMinute int
	RateLimitBurst     int
	MaxBodyBytes       int64
}

// Deps are the constructed dependencies for the API.
type Deps struct {
	DB           *database.DB
	Verifier     auth.Verifier
	DevIssuer    *auth.DevIssuer
	Orchestrator *executions.OrchestratorClient
	Logger       *zap.Logger
	Config       Config
}

// Mount registers /v1 routes on the provided router. Health routes remain owned by httpserver.
func Mount(r chi.Router, deps Deps) {
	orgStore := organizations.NewStore(deps.DB)
	orgHandlers := organizations.NewHandlers(orgStore)
	projectHandlers := projects.NewHandlers(projects.NewStore(deps.DB))
	agentHandlers := agents.NewHandlers(agents.NewStore(deps.DB))
	workflowHandlers := workflows.NewHandlers(workflows.NewStore(deps.DB))
	executionHandlers := executions.NewHandlers(executions.NewStore(deps.DB), deps.Orchestrator, deps.Logger)

	if deps.Config.MaxBodyBytes <= 0 {
		deps.Config.MaxBodyBytes = 1 << 20
	}

	r.Route("/v1", func(r chi.Router) {
		// Development-only. Never mounted in OIDC mode.
		if deps.DevIssuer != nil {
			r.Post("/dev/token", deps.DevIssuer.Handler)
		}

		r.Group(func(r chi.Router) {
			r.Use(auth.Authenticate(deps.Verifier))
			r.Use(middleware.RateLimit(deps.Config.RateLimitPerMinute, deps.Config.RateLimitBurst))
			r.Use(middleware.BodyLimit(deps.Config.MaxBodyBytes))

			r.Get("/orgs", orgHandlers.List)
			r.Post("/orgs", orgHandlers.Create)

			r.Route("/orgs/{org_id}", func(r chi.Router) {
				r.Use(orgHandlers.ResolveMembership)

				r.Get("/members", orgHandlers.ListMembers)
				r.Post("/members", orgHandlers.AddMember)
				r.Patch("/members/{principal_id}", orgHandlers.UpdateMember)
				r.Delete("/members/{principal_id}", orgHandlers.RemoveMember)

				r.Get("/projects", projectHandlers.List)
				r.Post("/projects", projectHandlers.Create)
				r.Get("/projects/{project_id}", projectHandlers.Get)

				r.Get("/projects/{project_id}/agents", agentHandlers.List)
				r.Post("/projects/{project_id}/agents", agentHandlers.Create)
				r.Get("/projects/{project_id}/agents/{agent_id}", agentHandlers.Get)
				r.Get("/projects/{project_id}/agents/{agent_id}/versions", agentHandlers.ListVersions)
				r.Post("/projects/{project_id}/agents/{agent_id}/versions", agentHandlers.CreateVersion)
				r.Get("/projects/{project_id}/agents/{agent_id}/versions/{version}", agentHandlers.GetVersion)
				r.Post("/projects/{project_id}/agents/{agent_id}/versions/{version}:publish", agentHandlers.PublishVersion)

				r.Get("/projects/{project_id}/workflows", workflowHandlers.List)
				r.Post("/projects/{project_id}/workflows", workflowHandlers.Create)
				r.Get("/projects/{project_id}/workflows/{workflow_id}", workflowHandlers.Get)
				r.Get("/projects/{project_id}/workflows/{workflow_id}/versions", workflowHandlers.ListVersions)
				r.Post("/projects/{project_id}/workflows/{workflow_id}/versions", workflowHandlers.CreateVersion)
				r.Get("/projects/{project_id}/workflows/{workflow_id}/versions/{version}", workflowHandlers.GetVersion)
				r.Post("/projects/{project_id}/workflows/{workflow_id}/versions/{version}:publish", workflowHandlers.PublishVersion)

				r.Post("/projects/{project_id}/executions", executionHandlers.Create)
				r.Get("/projects/{project_id}/executions", executionHandlers.List)
				r.Get("/projects/{project_id}/executions/{execution_id}", executionHandlers.Get)
				r.Post("/projects/{project_id}/executions/{execution_id}:cancel", executionHandlers.Cancel)
				r.Get("/projects/{project_id}/executions/{execution_id}/tasks", executionHandlers.ListTasks)
				r.Get("/projects/{project_id}/executions/{execution_id}/tasks/{task_id}", executionHandlers.GetTask)
				r.Get("/projects/{project_id}/executions/{execution_id}/events", executionHandlers.ListEvents)
			})
		})
	})
}
