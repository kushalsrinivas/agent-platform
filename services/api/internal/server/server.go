// Package server mounts the public API routes.
package server

import (
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/services/api/internal/agents"
	"github.com/agent-platform/agent-platform/services/api/internal/apikeys"
	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/database"
	"github.com/agent-platform/agent-platform/services/api/internal/developer"
	"github.com/agent-platform/agent-platform/services/api/internal/environments"
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

// Mount registers the public API. /api/v1 is canonical; /v1 remains an alias for existing clients.
func Mount(r chi.Router, deps Deps) {
	orgStore := organizations.NewStore(deps.DB)
	orgHandlers := organizations.NewHandlers(orgStore)
	projectHandlers := projects.NewHandlers(projects.NewStore(deps.DB))
	agentHandlers := agents.NewHandlers(agents.NewStore(deps.DB))
	workflowHandlers := workflows.NewHandlers(workflows.NewStore(deps.DB))
	executionHandlers := executions.NewHandlers(executions.NewStore(deps.DB), deps.Orchestrator, deps.Logger)
	keyHandlers := apikeys.NewHandlers(apikeys.NewStore(deps.DB))
	environmentHandlers := environments.NewHandlers(environments.NewStore(deps.DB))
	developerHandlers := developer.NewHandlers(developer.NewStore(deps.DB))

	if deps.Config.MaxBodyBytes <= 0 {
		deps.Config.MaxBodyBytes = 1 << 20
	}

	mount := func(r chi.Router) {
		if deps.DevIssuer != nil {
			r.Post("/dev/token", deps.DevIssuer.Handler)
		}

		r.Group(func(r chi.Router) {
			r.Use(auth.Authenticate(deps.Verifier))
			r.Use(middleware.RateLimit(deps.Config.RateLimitPerMinute, deps.Config.RateLimitBurst))
			r.Use(middleware.BodyLimit(deps.Config.MaxBodyBytes))

			mountOrganizations := func(r chi.Router, base string) {
				r.Get(base, orgHandlers.List)
				r.Post(base, orgHandlers.Create)

				r.Route(base+"/{org_id}", func(r chi.Router) {
					r.Use(orgHandlers.ResolveMembership)

					r.Get("/members", orgHandlers.ListMembers)
					r.Post("/members", orgHandlers.AddMember)
					r.Patch("/members/{principal_id}", orgHandlers.UpdateMember)
					r.Delete("/members/{principal_id}", orgHandlers.RemoveMember)

					r.Get("/api-keys", keyHandlers.List)
					r.Post("/api-keys", keyHandlers.Create)
					r.Delete("/api-keys/{key_id}", keyHandlers.Revoke)

					r.Get("/workers", developerHandlers.Workers)
					r.Get("/usage", developerHandlers.Usage)
					r.Get("/approvals", developerHandlers.Approvals)

					r.Get("/projects", projectHandlers.List)
					r.Post("/projects", projectHandlers.Create)
					r.Get("/projects/{project_id}", projectHandlers.Get)

					r.Get("/projects/{project_id}/environments", environmentHandlers.List)
					r.Post("/projects/{project_id}/environments", environmentHandlers.Create)
					r.Get("/projects/{project_id}/environments/{environment_id}", environmentHandlers.Get)

					r.Get("/projects/{project_id}/tools", developerHandlers.Tools)
					r.Get("/projects/{project_id}/mcp/servers", developerHandlers.MCPServers)

					r.Get("/projects/{project_id}/agents", agentHandlers.List)
					r.Post("/projects/{project_id}/agents", agentHandlers.Create)
					r.Get("/projects/{project_id}/agents/{agent_id}", agentHandlers.Get)
					r.Get("/projects/{project_id}/agents/{agent_id}/versions", agentHandlers.ListVersions)
					r.Post("/projects/{project_id}/agents/{agent_id}/versions", agentHandlers.CreateVersion)
					r.Get("/projects/{project_id}/agents/{agent_id}/versions/{version}", agentHandlers.GetVersion)
					r.Post("/projects/{project_id}/agents/{agent_id}/versions/{version}:publish", agentHandlers.PublishVersion)

					r.Get("/projects/{project_id}/workflows", workflowHandlers.List)
					r.Post("/projects/{project_id}/workflows", workflowHandlers.Create)
					r.Post("/projects/{project_id}/workflows:validate", workflowHandlers.Validate)
					r.Get("/projects/{project_id}/workflows/{workflow_id}", workflowHandlers.Get)
					r.Get("/projects/{project_id}/workflows/{workflow_id}/versions", workflowHandlers.ListVersions)
					r.Post("/projects/{project_id}/workflows/{workflow_id}/versions", workflowHandlers.CreateVersion)
					r.Get("/projects/{project_id}/workflows/{workflow_id}/versions/{version}", workflowHandlers.GetVersion)
					r.Post("/projects/{project_id}/workflows/{workflow_id}/versions/{version}:publish", workflowHandlers.PublishVersion)

					r.Post("/projects/{project_id}/executions", executionHandlers.Create)
					r.Get("/projects/{project_id}/executions", executionHandlers.List)
					r.Get("/projects/{project_id}/executions/{execution_id}", executionHandlers.Get)
					r.Post("/projects/{project_id}/executions/{execution_id}:cancel", executionHandlers.Cancel)
					r.Post("/projects/{project_id}/executions/{execution_id}:resume", executionHandlers.Resume)
					r.Get("/projects/{project_id}/executions/{execution_id}/input", executionHandlers.Input)
					r.Get("/projects/{project_id}/executions/{execution_id}/replay", executionHandlers.Replay)
					r.Post("/projects/{project_id}/executions/{execution_id}:replay", executionHandlers.Reexecute)
					r.Get("/projects/{project_id}/executions/{execution_id}/tasks", executionHandlers.ListTasks)
					r.Get("/projects/{project_id}/executions/{execution_id}/tasks/{task_id}", executionHandlers.GetTask)
					r.Post("/projects/{project_id}/executions/{execution_id}/tasks/{task_id}:approve", executionHandlers.ApproveTask)
					r.Post("/projects/{project_id}/executions/{execution_id}/tasks/{task_id}:reject", executionHandlers.RejectTask)
					r.Post("/projects/{project_id}/executions/{execution_id}/tasks/{task_id}:signal", executionHandlers.SignalTask)
					r.Get("/projects/{project_id}/executions/{execution_id}/approvals", executionHandlers.ListApprovals)
					r.Get("/projects/{project_id}/executions/{execution_id}/delegations", executionHandlers.ListDelegations)
					r.Get("/projects/{project_id}/executions/{execution_id}/events", executionHandlers.ListEvents)
					r.Get("/projects/{project_id}/executions/{execution_id}/stream", executionHandlers.Stream)
				})
			}
			mountOrganizations(r, "/orgs")
			mountOrganizations(r, "/organizations")
		})
	}

	r.Route("/v1", mount)
	r.Route("/api/v1", mount)
}
