// Command loadgen drives executions through the public API and reports throughput and latency from PostgreSQL.
//
// It is deliberately a workload generator, not a benchmark harness: every execution uses the deterministic echo
// provider unless a real provider is configured, so model latency does not pollute orchestration measurements.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	apiURL        string
	dbURL         string
	count         int
	concurrency   int
	pool          string
	provider      string
	model         string
	baseURL       string
	credential    string
	credentialEnv string
	instructions  string
	maxAttempts   int
	timeout       time.Duration
	reportPath    string
	cleanup       bool
	allowOpen     bool
	runID         string
}

type apiClient struct {
	base  string
	token string
	http  *http.Client
}

func main() {
	cfg := parseFlags()
	if err := run(context.Background(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	cfg := config{}
	flag.StringVar(&cfg.apiURL, "api", envOr("API_URL", "http://localhost:8080"), "API base URL")
	flag.StringVar(&cfg.dbURL, "db", envOr("POSTGRES_DATABASE_URL", "postgres://postgres:postgres@localhost:5432/agent_platform?sslmode=disable"), "PostgreSQL URL for result metrics")
	flag.IntVar(&cfg.count, "count", 100, "number of executions to submit")
	flag.IntVar(&cfg.concurrency, "concurrency", 32, "concurrent submissions")
	flag.StringVar(&cfg.pool, "pool", "default", "worker pool for the agent node")
	flag.StringVar(&cfg.provider, "provider", "echo", "model provider name")
	flag.StringVar(&cfg.model, "model", "echo-1", "model name")
	flag.StringVar(&cfg.baseURL, "base-url", "", "provider base URL for openai_compatible")
	flag.StringVar(&cfg.credential, "credential", "", "provider API key, stored as a project secret")
	flag.StringVar(&cfg.credentialEnv, "credential-env", "", "environment variable containing the provider API key")
	flag.StringVar(&cfg.instructions, "instructions", "Return the input.", "agent system instructions")
	flag.IntVar(&cfg.maxAttempts, "max-attempts", 2, "task retry budget")
	flag.DurationVar(&cfg.timeout, "timeout", 15*time.Minute, "maximum time to wait for completion")
	flag.StringVar(&cfg.reportPath, "report", "", "write the JSON report to this path")
	flag.BoolVar(&cfg.cleanup, "cleanup", true, "delete the test organisation after the report")
	flag.BoolVar(&cfg.allowOpen, "allow-open", false, "exit successfully when executions are still open at the timeout")
	flag.StringVar(&cfg.runID, "run-id", "", "run identifier (default: random)")
	flag.Parse()
	if cfg.runID == "" {
		cfg.runID = strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	}
	if cfg.credential == "" && cfg.credentialEnv != "" {
		cfg.credential = os.Getenv(cfg.credentialEnv)
		if cfg.credential == "" {
			fmt.Fprintf(os.Stderr, "loadgen: %s is empty\n", cfg.credentialEnv)
			os.Exit(2)
		}
	}
	return cfg
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

type fixture struct {
	orgID      string
	projectID  string
	workflowID string
}

func run(ctx context.Context, cfg config) error {
	pool, err := pgxpool.New(ctx, cfg.dbURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	api := &apiClient{
		base: strings.TrimRight(cfg.apiURL, "/"),
		http: &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{
			MaxIdleConns:        200,
			MaxIdleConnsPerHost: 200,
		}},
	}
	if err := api.devToken(ctx, "load_"+cfg.runID); err != nil {
		return err
	}
	fx, err := api.setup(ctx, cfg)
	if err != nil {
		return err
	}
	if cfg.cleanup {
		defer func() {
			if cleanupErr := cleanupOrg(context.Background(), pool, fx.orgID); cleanupErr != nil {
				fmt.Fprintln(os.Stderr, "cleanup:", cleanupErr)
			}
		}()
	}

	startedAt := time.Now().UTC()
	peak := &peakSampler{pool: pool, orgID: fx.orgID}
	peak.start(ctx)
	submission := submit(ctx, api, fx, cfg)
	completion := waitForCompletion(ctx, pool, fx.orgID, submission.Accepted, cfg.timeout)
	peak.stop()
	finishedAt := time.Now().UTC()

	metrics, err := loadMetrics(ctx, pool, fx.orgID)
	if err != nil {
		return fmt.Errorf("collect metrics: %w", err)
	}
	report := buildReport(cfg, submission, completion, peak, metrics, startedAt, finishedAt)
	printReport(report)
	if cfg.reportPath != "" {
		raw, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(cfg.reportPath, append(raw, '\n'), 0o644); err != nil { //nolint:gosec // report is intentionally readable.
			return err
		}
		fmt.Println("report:", cfg.reportPath)
	}
	if completion.TimedOut && !cfg.allowOpen {
		return fmt.Errorf("%d executions were still open after %s", completion.Open, cfg.timeout)
	}
	return nil
}

func (c *apiClient) do(ctx context.Context, method, path string, body any, key string) (int, map[string]any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	var decoded map[string]any
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &decoded)
	}
	return response.StatusCode, decoded, nil
}

func (c *apiClient) devToken(ctx context.Context, principal string) error {
	status, body, err := c.do(ctx, http.MethodPost, "/v1/dev/token", map[string]any{"principal_id": principal}, "")
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("dev token status %d", status)
	}
	c.token, _ = body["access_token"].(string)
	if c.token == "" {
		return fmt.Errorf("dev token response has no access_token")
	}
	return nil
}

func (c *apiClient) setup(ctx context.Context, cfg config) (fixture, error) {
	var fx fixture
	status, body, err := c.do(ctx, http.MethodPost, "/v1/orgs", map[string]any{
		"name": "Load Test " + cfg.runID, "slug": "load-" + cfg.runID,
	}, "")
	if err != nil {
		return fx, err
	}
	if status != http.StatusCreated {
		return fx, fmt.Errorf("create org status %d: %v", status, body)
	}
	fx.orgID = stringField(body, "id")

	status, body, err = c.do(ctx, http.MethodPost, "/v1/orgs/"+fx.orgID+"/projects", map[string]any{
		"name": "Load Project", "slug": "load-project",
	}, "")
	if err != nil {
		return fx, err
	}
	if status != http.StatusCreated {
		return fx, fmt.Errorf("create project status %d: %v", status, body)
	}
	fx.projectID = stringField(body, "id")

	credentialRef := "default"
	parameters := map[string]any{}
	if cfg.baseURL != "" {
		parameters["base_url"] = cfg.baseURL
	}
	if cfg.credential != "" {
		credentialRef = "load_provider_key"
		if status, body, err = c.do(ctx, http.MethodPost,
			"/v1/orgs/"+fx.orgID+"/projects/"+fx.projectID+"/secrets",
			map[string]any{"name": credentialRef, "value": cfg.credential}, ""); err != nil {
			return fx, err
		}
		if status != http.StatusCreated {
			return fx, fmt.Errorf("create secret status %d: %v", status, body)
		}
	}

	agent := map[string]any{
		"name": "load-agent", "description": "load generator agent",
		"version": map[string]any{"spec": map[string]any{
			"model": map[string]any{
				"provider": cfg.provider, "name": cfg.model, "credential_ref": credentialRef,
				"parameters": parameters,
			},
			"instructions": cfg.instructions,
			"runtime": map[string]any{
				"abi_version": 1, "image_digest": "sha256:" + strings.Repeat("a", 64),
			},
			"limits":     map[string]any{"max_turns": 4, "timeout_seconds": 120},
			"checkpoint": map[string]any{"mode": "on_node_end", "require_checkpoint": false},
		}},
	}
	status, body, err = c.do(ctx, http.MethodPost, "/v1/orgs/"+fx.orgID+"/projects/"+fx.projectID+"/agents", agent, "")
	if err != nil {
		return fx, err
	}
	if status != http.StatusCreated {
		return fx, fmt.Errorf("create agent status %d: %v", status, body)
	}
	agentID := stringField(body, "id")
	status, body, err = c.do(ctx, http.MethodPost,
		"/v1/orgs/"+fx.orgID+"/projects/"+fx.projectID+"/agents/"+agentID+"/versions/1:publish", nil, "")
	if err != nil || status != http.StatusOK {
		return fx, fmt.Errorf("publish agent status %d: %v (%w)", status, body, err)
	}

	workflow := map[string]any{
		"name": "load-flow",
		"version": map[string]any{"graph": map[string]any{
			"nodes": []any{map[string]any{
				"id": "agent", "type": "agent",
				"agent":       map[string]any{"agent_id": agentID, "version": 1},
				"worker_pool": cfg.pool, "timeout_seconds": 120,
				"retry": map[string]any{"max_attempts": cfg.maxAttempts, "retry_on": []any{"worker_lost", "transient"}},
			}},
			"edges":        []any{},
			"policy":       map[string]any{"on_failure": "fail_fast"},
			"input_schema": map[string]any{"type": "object", "required": []any{"topic"}},
		}},
	}
	status, body, err = c.do(ctx, http.MethodPost,
		"/v1/orgs/"+fx.orgID+"/projects/"+fx.projectID+"/workflows", workflow, "")
	if err != nil {
		return fx, err
	}
	if status != http.StatusCreated {
		return fx, fmt.Errorf("create workflow status %d: %v", status, body)
	}
	fx.workflowID = stringField(body, "id")
	status, body, err = c.do(ctx, http.MethodPost,
		"/v1/orgs/"+fx.orgID+"/projects/"+fx.projectID+"/workflows/"+fx.workflowID+"/versions/1:publish", nil, "")
	if err != nil || status != http.StatusOK {
		return fx, fmt.Errorf("publish workflow status %d: %v (%w)", status, body, err)
	}
	return fx, nil
}

func (c *apiClient) createExecution(ctx context.Context, fx fixture, key string) (int, string, error) {
	for attempt := 0; attempt < 6; attempt++ {
		status, body, err := c.do(ctx, http.MethodPost,
			"/v1/orgs/"+fx.orgID+"/projects/"+fx.projectID+"/executions",
			map[string]any{
				"workflow_id": fx.workflowID, "workflow_version": 1,
				"input": map[string]any{"topic": "load " + key},
			}, key)
		if err != nil {
			return 0, "", err
		}
		if status != http.StatusTooManyRequests {
			return status, stringField(body, "id"), nil
		}
		time.Sleep(time.Duration(50*(attempt+1)) * time.Millisecond)
	}
	return http.StatusTooManyRequests, "", nil
}

// cleanupOrg removes load-test data directly. There is deliberately no organisation-delete API endpoint, and
// this binary already holds the local administrative database URL.
func cleanupOrg(ctx context.Context, pool *pgxpool.Pool, orgID string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := []string{
		`DELETE FROM execution.executions WHERE org_id = $1`,
		`DELETE FROM control.workflow_versions WHERE org_id = $1`,
		`DELETE FROM control.workflows WHERE org_id = $1`,
		`DELETE FROM control.agent_versions WHERE org_id = $1`,
		`DELETE FROM control.agents WHERE org_id = $1`,
		`DELETE FROM control.memberships WHERE org_id = $1`,
		`DELETE FROM control.api_keys WHERE org_id = $1`,
		`DELETE FROM control.credentials WHERE org_id = $1`,
		`DELETE FROM control.audit_events WHERE org_id = $1`,
		`DELETE FROM control.idempotency_keys WHERE org_id = $1`,
		`DELETE FROM control.projects WHERE org_id = $1`,
		`DELETE FROM control.organizations WHERE id = $1`,
	}
	for _, query := range queries {
		if _, err := tx.Exec(ctx, query, orgID); err != nil {
			return err
		}
	}
	// Deleting in-flight executions bypasses the normal slot release. Load-test cleanup is destructive and
	// single-tenant, so repair the counters before the next run.
	if _, err := tx.Exec(ctx, `
		UPDATE execution.workers
		SET reserved_slots = 0,
		    status = CASE WHEN status = 'busy' THEN 'available' ELSE status END,
		    updated_at = now()`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func stringField(body map[string]any, field string) string {
	value, _ := body[field].(string)
	return value
}

type submission struct {
	Attempted int
	Accepted  int
	Rejected  int
	Errors    int
	Elapsed   time.Duration
	Latencies []float64   `json:"-"`
	Statuses  map[int]int `json:"-"`
}

type submissionResult struct {
	status  int
	latency time.Duration
	err     error
}

func submit(ctx context.Context, api *apiClient, fx fixture, cfg config) submission {
	results := submission{Attempted: cfg.count, Statuses: map[int]int{}, Latencies: make([]float64, 0, cfg.count)}
	started := time.Now()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var accepted atomic.Int64
	workers := cfg.concurrency
	if workers < 1 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				start := time.Now()
				status, _, err := api.createExecution(ctx, fx, fmt.Sprintf("load-%s-%d", cfg.runID, index))
				result := submissionResult{status: status, latency: time.Since(start), err: err}
				mu.Lock()
				results.Statuses[result.status]++
				results.Latencies = append(results.Latencies, result.latency.Seconds())
				if result.err != nil {
					results.Errors++
				} else if result.status == http.StatusAccepted || result.status == http.StatusOK {
					results.Accepted++
					accepted.Add(1)
				} else {
					results.Rejected++
				}
				mu.Unlock()
			}
		}()
	}
	for i := 0; i < cfg.count; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	results.Elapsed = time.Since(started)
	sort.Float64s(results.Latencies)
	return results
}

type completion struct {
	Succeeded int
	Failed    int
	Cancelled int
	Open      int
	TimedOut  bool
}

func waitForCompletion(ctx context.Context, pool *pgxpool.Pool, orgID string, expected int, timeout time.Duration) completion {
	deadline := time.Now().Add(timeout)
	var last completion
	for {
		last = queryCompletion(ctx, pool, orgID)
		if last.Succeeded+last.Failed+last.Cancelled >= expected || time.Now().After(deadline) {
			last.Open = expected - (last.Succeeded + last.Failed + last.Cancelled)
			last.TimedOut = last.Open > 0
			return last
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func queryCompletion(ctx context.Context, pool *pgxpool.Pool, orgID string) completion {
	var state completion
	_ = pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE state = 'succeeded'),
		       count(*) FILTER (WHERE state = 'failed'),
		       count(*) FILTER (WHERE state = 'cancelled')
		FROM execution.executions WHERE org_id = $1`, orgID).
		Scan(&state.Succeeded, &state.Failed, &state.Cancelled)
	return state
}

type peakSampler struct {
	pool       *pgxpool.Pool
	orgID      string
	stopCh     chan struct{}
	done       chan struct{}
	executions atomic.Int64
	tasks      atomic.Int64
}

func (p *peakSampler) start(ctx context.Context) {
	p.stopCh = make(chan struct{})
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			var executions, tasks int64
			_ = p.pool.QueryRow(ctx, `
				SELECT count(*) FROM execution.executions
				WHERE org_id = $1 AND state IN ('pending', 'running', 'failing', 'cancel_requested')`,
				p.orgID).Scan(&executions)
			_ = p.pool.QueryRow(ctx, `
				SELECT count(*) FROM execution.execution_tasks
				WHERE org_id = $1 AND state = 'pending'`, p.orgID).Scan(&tasks)
			updateMax(&p.executions, executions)
			updateMax(&p.tasks, tasks)
			select {
			case <-p.stopCh:
				return
			case <-ticker.C:
			}
		}
	}()
}

func (p *peakSampler) stop() {
	if p.stopCh == nil {
		return
	}
	close(p.stopCh)
	<-p.done
}

func updateMax(target *atomic.Int64, value int64) {
	for {
		current := target.Load()
		if value <= current || target.CompareAndSwap(current, value) {
			return
		}
	}
}

type metrics struct {
	Executions        int     `json:"executions"`
	Succeeded         int     `json:"succeeded"`
	Failed            int     `json:"failed"`
	Cancelled         int     `json:"cancelled"`
	Open              int     `json:"open"`
	EndToEndP50       float64 `json:"end_to_end_p50_seconds"`
	EndToEndP95       float64 `json:"end_to_end_p95_seconds"`
	EndToEndP99       float64 `json:"end_to_end_p99_seconds"`
	Tasks             int     `json:"tasks"`
	QueueWaitP50      float64 `json:"queue_wait_p50_seconds"`
	QueueWaitP95      float64 `json:"queue_wait_p95_seconds"`
	QueueWaitP99      float64 `json:"queue_wait_p99_seconds"`
	Attempts          int     `json:"attempts"`
	RetriedAttempts   int     `json:"retried_attempts"`
	TaskAttemptsAvg   float64 `json:"task_attempts_avg"`
	AttemptLatencyP95 float64 `json:"attempt_latency_p95_seconds"`
}

func loadMetrics(ctx context.Context, pool *pgxpool.Pool, orgID string) (metrics, error) {
	var m metrics
	if err := pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE state = 'succeeded'),
		       count(*) FILTER (WHERE state = 'failed'),
		       count(*) FILTER (WHERE state = 'cancelled'),
		       count(*) FILTER (WHERE state IN ('pending', 'running', 'failing', 'cancel_requested')),
		       COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM ended_at - created_at))
		                FILTER (WHERE ended_at IS NOT NULL AND state = 'succeeded'), 0),
		       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM ended_at - created_at))
		                FILTER (WHERE ended_at IS NOT NULL AND state = 'succeeded'), 0),
		       COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY extract(epoch FROM ended_at - created_at))
		                FILTER (WHERE ended_at IS NOT NULL AND state = 'succeeded'), 0)
		FROM execution.executions WHERE org_id = $1`, orgID).
		Scan(&m.Executions, &m.Succeeded, &m.Failed, &m.Cancelled, &m.Open,
			&m.EndToEndP50, &m.EndToEndP95, &m.EndToEndP99); err != nil {
		return m, err
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*),
		       COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM started_at - created_at))
		                FILTER (WHERE started_at IS NOT NULL), 0),
		       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM started_at - created_at))
		                FILTER (WHERE started_at IS NOT NULL), 0),
		       COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY extract(epoch FROM started_at - created_at))
		                FILTER (WHERE started_at IS NOT NULL), 0),
		       COALESCE(avg(attempt_count), 0),
		       COALESCE(sum(GREATEST(attempt_count - 1, 0)), 0)
		FROM execution.execution_tasks WHERE org_id = $1`, orgID).
		Scan(&m.Tasks, &m.QueueWaitP50, &m.QueueWaitP95, &m.QueueWaitP99,
			&m.TaskAttemptsAvg, &m.RetriedAttempts); err != nil {
		return m, err
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE attempt_number > 1),
		       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM finished_at - started_at))
		                FILTER (WHERE finished_at IS NOT NULL AND started_at IS NOT NULL), 0)
		FROM execution.task_attempts WHERE org_id = $1`, orgID).
		Scan(&m.Attempts, &m.RetriedAttempts, &m.AttemptLatencyP95); err != nil {
		return m, err
	}
	return m, nil
}

type reportConfig struct {
	APIURL      string `json:"api_url"`
	Count       int    `json:"count"`
	Concurrency int    `json:"concurrency"`
	Pool        string `json:"pool"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	MaxAttempts int    `json:"max_attempts"`
	Timeout     string `json:"timeout"`
}

type report struct {
	RunID                 string       `json:"run_id"`
	StartedAt             time.Time    `json:"started_at"`
	FinishedAt            time.Time    `json:"finished_at"`
	Duration              float64      `json:"duration_seconds"`
	Config                reportConfig `json:"config"`
	Submission            submission   `json:"submission"`
	SubmitP50             float64      `json:"submit_p50_seconds"`
	SubmitP95             float64      `json:"submit_p95_seconds"`
	SubmitP99             float64      `json:"submit_p99_seconds"`
	Completion            completion   `json:"completion"`
	PeakPendingExecutions int64        `json:"peak_pending_executions"`
	PeakPendingTasks      int64        `json:"peak_pending_tasks"`
	Metrics               metrics      `json:"metrics"`
	SubmissionRate        float64      `json:"submission_rate_per_second"`
	CompletionRate        float64      `json:"completion_rate_per_second"`
	TaskRate              float64      `json:"task_rate_per_second"`
}

func buildReport(cfg config, sub submission, done completion, peak *peakSampler, m metrics, started, finished time.Time) report {
	elapsed := finished.Sub(started).Seconds()
	rep := report{
		RunID: cfg.runID, StartedAt: started, FinishedAt: finished, Duration: elapsed,
		Config: reportConfig{
			APIURL: cfg.apiURL, Count: cfg.count, Concurrency: cfg.concurrency, Pool: cfg.pool,
			Provider: cfg.provider, Model: cfg.model, MaxAttempts: cfg.maxAttempts, Timeout: cfg.timeout.String(),
		},
		Submission: sub, Completion: done,
		SubmitP50: percentile(sub.Latencies, 0.50), SubmitP95: percentile(sub.Latencies, 0.95),
		SubmitP99:             percentile(sub.Latencies, 0.99),
		PeakPendingExecutions: peak.executions.Load(), PeakPendingTasks: peak.tasks.Load(),
		Metrics: m,
	}
	if elapsed > 0 {
		rep.SubmissionRate = float64(sub.Accepted) / elapsed
		rep.CompletionRate = float64(done.Succeeded+done.Failed+done.Cancelled) / elapsed
		rep.TaskRate = float64(m.Tasks) / elapsed
	}
	return rep
}

func percentile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(q * float64(len(values)-1))
	return values[index]
}

func printReport(rep report) {
	fmt.Printf("Load run %s — %d executions, %d concurrent submissions\n", rep.RunID, rep.Config.Count, rep.Config.Concurrency)
	fmt.Printf("Submission: %d accepted, %d rejected, %d errors in %.3fs (%.1f/s) statuses=%v\n",
		rep.Submission.Accepted, rep.Submission.Rejected, rep.Submission.Errors,
		rep.Submission.Elapsed.Seconds(), float64(rep.Submission.Accepted)/rep.Submission.Elapsed.Seconds(),
		rep.Submission.Statuses)
	fmt.Printf("Submit latency: p50=%.0fms p95=%.0fms p99=%.0fms\n",
		rep.SubmitP50*1000, rep.SubmitP95*1000, rep.SubmitP99*1000)
	fmt.Printf("Completion: %d succeeded, %d failed, %d cancelled, %d open in %.1fs (%.1f/s)\n",
		rep.Completion.Succeeded, rep.Completion.Failed, rep.Completion.Cancelled, rep.Completion.Open,
		rep.Duration, rep.CompletionRate)
	fmt.Printf("End-to-end: p50=%.2fs p95=%.2fs p99=%.2fs\n",
		rep.Metrics.EndToEndP50, rep.Metrics.EndToEndP95, rep.Metrics.EndToEndP99)
	fmt.Printf("Queue wait: p50=%.2fs p95=%.2fs p99=%.2fs\n",
		rep.Metrics.QueueWaitP50, rep.Metrics.QueueWaitP95, rep.Metrics.QueueWaitP99)
	fmt.Printf("Tasks: %d, attempts=%d, retried=%d, avg attempts=%.2f, attempt p95=%.2fs\n",
		rep.Metrics.Tasks, rep.Metrics.Attempts, rep.Metrics.RetriedAttempts,
		rep.Metrics.TaskAttemptsAvg, rep.Metrics.AttemptLatencyP95)
	fmt.Printf("Peak pending: executions=%d tasks=%d; task throughput=%.1f/s\n",
		rep.PeakPendingExecutions, rep.PeakPendingTasks, rep.TaskRate)
}
