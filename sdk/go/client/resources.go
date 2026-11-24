package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ExecuteOptions tune workflow submission.
type ExecuteOptions struct {
	Priority       int
	DeadlineAt     *time.Time
	IdempotencyKey string
}

func (c *Client) ListOrganizations(ctx context.Context) ([]Organization, error) {
	var page Page[Organization]
	if err := c.do(ctx, http.MethodGet, "/api/v1/organizations", nil, nil, "", &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (c *Client) CreateOrganization(ctx context.Context, name, slug string) (Organization, error) {
	var out Organization
	err := c.do(ctx, http.MethodPost, "/api/v1/organizations",
		nil, map[string]any{"name": name, "slug": slug}, "", &out)
	return out, err
}

func (c *Client) ListProjects(ctx context.Context, orgID string) ([]Project, error) {
	base, err := c.orgScope(orgID)
	if err != nil {
		return nil, err
	}
	var page Page[Project]
	if err := c.do(ctx, http.MethodGet, base+"/projects", nil, nil, "", &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (c *Client) CreateProject(ctx context.Context, orgID, name, slug string) (Project, error) {
	base, err := c.orgScope(orgID)
	if err != nil {
		return Project{}, err
	}
	var out Project
	err = c.do(ctx, http.MethodPost, base+"/projects",
		nil, map[string]any{"name": name, "slug": slug}, "", &out)
	return out, err
}

func (c *Client) CreateEnvironment(ctx context.Context, projectID, name, slug, kind string) (Environment, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return Environment{}, err
	}
	body := map[string]any{"name": name, "kind": kind}
	if slug != "" {
		body["slug"] = slug
	}
	var out Environment
	err = c.do(ctx, http.MethodPost, base+"/environments", nil, body, "", &out)
	return out, err
}

func (c *Client) ListEnvironments(ctx context.Context, projectID string) ([]Environment, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return nil, err
	}
	var page Page[Environment]
	if err := c.do(ctx, http.MethodGet, base+"/environments", nil, nil, "", &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (c *Client) CreateAgent(ctx context.Context, projectID, name, description string, spec any) (Agent, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return Agent{}, err
	}
	var out Agent
	err = c.do(ctx, http.MethodPost, base+"/agents", nil, map[string]any{
		"name": name, "description": description, "version": map[string]any{"spec": spec},
	}, "", &out)
	return out, err
}

func (c *Client) CreateAgentVersion(ctx context.Context, projectID, agentID string, spec any) (AgentVersion, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return AgentVersion{}, err
	}
	var out AgentVersion
	err = c.do(ctx, http.MethodPost, base+"/agents/"+url.PathEscape(agentID)+"/versions",
		nil, map[string]any{"spec": spec}, "", &out)
	return out, err
}

func (c *Client) GetAgent(ctx context.Context, projectID, agentID string) (Agent, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return Agent{}, err
	}
	var out Agent
	err = c.do(ctx, http.MethodGet, base+"/agents/"+url.PathEscape(agentID), nil, nil, "", &out)
	return out, err
}

func (c *Client) DeployAgent(ctx context.Context, projectID, agentID string, version int) (AgentVersion, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return AgentVersion{}, err
	}
	var out AgentVersion
	err = c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/agents/%s/versions/%d:publish", base, url.PathEscape(agentID), version),
		nil, nil, "", &out)
	return out, err
}

func (c *Client) ListWorkflows(ctx context.Context, projectID string, limit int) ([]Workflow, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var page Page[Workflow]
	if err := c.do(ctx, http.MethodGet, base+"/workflows", query, nil, "", &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (c *Client) GetWorkflow(ctx context.Context, projectID, workflowID string) (Workflow, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return Workflow{}, err
	}
	var out Workflow
	err = c.do(ctx, http.MethodGet, base+"/workflows/"+url.PathEscape(workflowID), nil, nil, "", &out)
	return out, err
}

func (c *Client) CreateWorkflow(ctx context.Context, projectID, name, description string, graph any) (Workflow, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return Workflow{}, err
	}
	var out Workflow
	err = c.do(ctx, http.MethodPost, base+"/workflows", nil, map[string]any{
		"name": name, "description": description, "version": map[string]any{"graph": graph},
	}, "", &out)
	return out, err
}

func (c *Client) ValidateWorkflow(ctx context.Context, projectID string, graph any) (ValidationResult, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return ValidationResult{}, err
	}
	var out ValidationResult
	err = c.do(ctx, http.MethodPost, base+"/workflows:validate", nil, map[string]any{"graph": graph}, "", &out)
	return out, err
}

func (c *Client) CreateWorkflowVersion(ctx context.Context, projectID, workflowID string,
	graph any) (WorkflowVersion, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return WorkflowVersion{}, err
	}
	var out WorkflowVersion
	err = c.do(ctx, http.MethodPost, base+"/workflows/"+url.PathEscape(workflowID)+"/versions",
		nil, map[string]any{"graph": graph}, "", &out)
	return out, err
}

func (c *Client) DeployWorkflow(ctx context.Context, projectID, workflowID string, version int) (WorkflowVersion, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return WorkflowVersion{}, err
	}
	var out WorkflowVersion
	err = c.do(ctx, http.MethodPost,
		fmt.Sprintf("%s/workflows/%s/versions/%d:publish", base, url.PathEscape(workflowID), version),
		nil, nil, "", &out)
	return out, err
}

func (c *Client) ExecuteWorkflow(ctx context.Context, projectID, workflowID string, version int,
	input map[string]any, options ExecuteOptions) (Execution, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return Execution{}, err
	}
	key := options.IdempotencyKey
	if key == "" {
		key = NewIdempotencyKey()
	}
	body := map[string]any{
		"workflow_id": workflowID, "workflow_version": version, "input": input, "priority": options.Priority,
	}
	if options.DeadlineAt != nil {
		body["deadline_at"] = options.DeadlineAt.UTC().Format(time.RFC3339Nano)
	}
	var out Execution
	err = c.do(ctx, http.MethodPost, base+"/executions", nil, body, key, &out)
	return out, err
}

func (c *Client) GetExecution(ctx context.Context, projectID, executionID string) (Execution, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return Execution{}, err
	}
	var out Execution
	err = c.do(ctx, http.MethodGet, base+"/executions/"+url.PathEscape(executionID), nil, nil, "", &out)
	return out, err
}

// ExecutionFilter filters and sorts an execution list.
type ExecutionFilter struct {
	State         string
	WorkflowID    string
	Sort          string
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
	Limit         int
	Cursor        string
}

func (c *Client) ListExecutions(ctx context.Context, projectID string,
	filter ExecutionFilter) (Page[Execution], error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return Page[Execution]{}, err
	}
	query := url.Values{}
	if filter.State != "" {
		query.Set("state", filter.State)
	}
	if filter.WorkflowID != "" {
		query.Set("workflow_id", filter.WorkflowID)
	}
	if filter.Sort != "" {
		query.Set("sort", filter.Sort)
	}
	if filter.CreatedAfter != nil {
		query.Set("created_after", filter.CreatedAfter.UTC().Format(time.RFC3339))
	}
	if filter.CreatedBefore != nil {
		query.Set("created_before", filter.CreatedBefore.UTC().Format(time.RFC3339))
	}
	if filter.Limit > 0 {
		query.Set("limit", strconv.Itoa(filter.Limit))
	}
	if filter.Cursor != "" {
		query.Set("cursor", filter.Cursor)
	}
	var out Page[Execution]
	err = c.do(ctx, http.MethodGet, base+"/executions", query, nil, "", &out)
	return out, err
}

func (c *Client) CancelExecution(ctx context.Context, projectID, executionID, reason string) (Execution, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return Execution{}, err
	}
	var out Execution
	err = c.do(ctx, http.MethodPost, base+"/executions/"+url.PathEscape(executionID)+":cancel",
		nil, map[string]any{"reason": reason}, "", &out)
	return out, err
}

func (c *Client) ResumeExecution(ctx context.Context, projectID, executionID string) (Execution, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return Execution{}, err
	}
	var out Execution
	err = c.do(ctx, http.MethodPost, base+"/executions/"+url.PathEscape(executionID)+":resume",
		nil, map[string]any{}, NewIdempotencyKey(), &out)
	return out, err
}

func (c *Client) WaitExecution(ctx context.Context, projectID, executionID string,
	timeout, poll time.Duration) (Execution, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	for {
		execution, err := c.GetExecution(ctx, projectID, executionID)
		if err != nil {
			return Execution{}, err
		}
		switch execution.State {
		case "succeeded", "failed", "cancelled":
			return execution, nil
		}
		if time.Now().After(deadline) {
			return Execution{}, fmt.Errorf("execution %s did not finish within %s (state=%s)",
				executionID, timeout, execution.State)
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Execution{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) ListTasks(ctx context.Context, projectID, executionID, state string) ([]Task, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	if state != "" {
		query.Set("state", state)
	}
	var page Page[Task]
	if err := c.do(ctx, http.MethodGet, base+"/executions/"+url.PathEscape(executionID)+"/tasks",
		query, nil, "", &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (c *Client) GetTask(ctx context.Context, projectID, executionID, taskID string) (TaskDetail, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return TaskDetail{}, err
	}
	var out TaskDetail
	err = c.do(ctx, http.MethodGet,
		base+"/executions/"+url.PathEscape(executionID)+"/tasks/"+url.PathEscape(taskID),
		nil, nil, "", &out)
	return out, err
}

func (c *Client) ListEvents(ctx context.Context, projectID, executionID string, after int64) ([]Event, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return nil, err
	}
	query := url.Values{"after": {strconv.FormatInt(after, 10)}, "limit": {"500"}}
	var page Page[Event]
	if err := c.do(ctx, http.MethodGet, base+"/executions/"+url.PathEscape(executionID)+"/events",
		query, nil, "", &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

// StreamEvents follows execution SSE events until the server closes the terminal stream.
func (c *Client) StreamEvents(ctx context.Context, projectID, executionID string, after int64,
	handle func(Event) error) error {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return err
	}
	path := base + "/executions/" + url.PathEscape(executionID) + "/stream?after=" + strconv.FormatInt(after, 10)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "text/event-stream")
	if c.apiKey != "" {
		request.Header.Set("X-API-Key", c.apiKey)
	} else if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return decodeAPIError(response.StatusCode, raw)
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	var data bytes.Buffer
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() > 0 {
				var event Event
				if err := json.Unmarshal(data.Bytes(), &event); err == nil {
					if err := handle(event); err != nil {
						return err
					}
				}
				data.Reset()
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return scanner.Err()
}

func (c *Client) ListApprovals(ctx context.Context, projectID, status string, limit int) ([]Approval, error) {
	base, err := c.orgScope("")
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	if projectID != "" {
		query.Set("project_id", projectID)
	}
	if status != "" {
		query.Set("status", status)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var page Page[Approval]
	if err := c.do(ctx, http.MethodGet, base+"/approvals", query, nil, "", &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (c *Client) ApproveTask(ctx context.Context, projectID, executionID, taskID, comment string) error {
	return c.taskSignal(ctx, projectID, executionID, taskID, "approve", map[string]any{"comment": comment})
}

func (c *Client) RejectTask(ctx context.Context, projectID, executionID, taskID, reason string) error {
	return c.taskSignal(ctx, projectID, executionID, taskID, "reject", map[string]any{"comment": reason})
}

func (c *Client) taskSignal(ctx context.Context, projectID, executionID, taskID, action string, body any) error {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost,
		base+"/executions/"+url.PathEscape(executionID)+"/tasks/"+url.PathEscape(taskID)+":"+action,
		nil, body, "", nil)
}

func (c *Client) ListTools(ctx context.Context, projectID string) ([]Tool, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return nil, err
	}
	var page Page[Tool]
	if err := c.do(ctx, http.MethodGet, base+"/tools", nil, nil, "", &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (c *Client) ListMCPServers(ctx context.Context, projectID string) ([]MCPServer, error) {
	base, err := c.projectScope("", projectID)
	if err != nil {
		return nil, err
	}
	var page Page[MCPServer]
	if err := c.do(ctx, http.MethodGet, base+"/mcp/servers", nil, nil, "", &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (c *Client) ListWorkers(ctx context.Context, pool, status string) ([]Worker, error) {
	base, err := c.orgScope("")
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	if pool != "" {
		query.Set("pool", pool)
	}
	if status != "" {
		query.Set("status", status)
	}
	var page Page[Worker]
	if err := c.do(ctx, http.MethodGet, base+"/workers", query, nil, "", &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (c *Client) GetUsage(ctx context.Context, projectID string, from, to time.Time) (Usage, error) {
	base, err := c.orgScope("")
	if err != nil {
		return Usage{}, err
	}
	query := url.Values{}
	if projectID != "" {
		query.Set("project_id", projectID)
	}
	if !from.IsZero() {
		query.Set("from", from.UTC().Format(time.RFC3339))
	}
	if !to.IsZero() {
		query.Set("to", to.UTC().Format(time.RFC3339))
	}
	var out Usage
	err = c.do(ctx, http.MethodGet, base+"/usage", query, nil, "", &out)
	return out, err
}

func (c *Client) CreateAPIKey(ctx context.Context, projectID, name, role string) (APIKey, error) {
	base, err := c.orgScope("")
	if err != nil {
		return APIKey{}, err
	}
	body := map[string]any{"name": name, "role": role}
	if projectID != "" {
		body["project_id"] = projectID
	}
	var out APIKey
	err = c.do(ctx, http.MethodPost, base+"/api-keys", nil, body, "", &out)
	return out, err
}

func (c *Client) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	base, err := c.orgScope("")
	if err != nil {
		return nil, err
	}
	var page Page[APIKey]
	if err := c.do(ctx, http.MethodGet, base+"/api-keys", nil, nil, "", &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (c *Client) RevokeAPIKey(ctx context.Context, keyID string) (APIKey, error) {
	base, err := c.orgScope("")
	if err != nil {
		return APIKey{}, err
	}
	var out APIKey
	err = c.do(ctx, http.MethodDelete, base+"/api-keys/"+url.PathEscape(keyID), nil, nil, "", &out)
	return out, err
}
