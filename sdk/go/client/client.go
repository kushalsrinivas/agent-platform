package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config configures the developer SDK. Scope values can be overridden per call.
type Config struct {
	BaseURL    string
	APIKey     string
	Token      string
	OrgID      string
	ProjectID  string
	Timeout    time.Duration
	MaxRetries int
	HTTPClient *http.Client
}

// Client calls the platform API.
type Client struct {
	baseURL    string
	apiKey     string
	token      string
	orgID      string
	projectID  string
	http       *http.Client
	maxRetries int
}

// New builds a client for baseURL using environment defaults for credentials and scope.
func New(baseURL string) *Client {
	return NewWithConfig(Config{BaseURL: baseURL})
}

// NewWithConfig builds a configured client. Missing values fall back to AGENT_PLATFORM_* environment variables.
func NewWithConfig(cfg Config) *Client {
	baseURL := firstNonEmpty(cfg.BaseURL, getenv("AGENT_PLATFORM_URL"), "http://localhost:8080")
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	retries := cfg.MaxRetries
	if retries < 0 {
		retries = 0
	}
	if retries == 0 && cfg.HTTPClient == nil {
		retries = 2
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     firstNonEmpty(cfg.APIKey, getenv("AGENT_PLATFORM_API_KEY")),
		token:      firstNonEmpty(cfg.Token, getenv("AGENT_PLATFORM_TOKEN")),
		orgID:      firstNonEmpty(cfg.OrgID, getenv("AGENT_PLATFORM_ORG")),
		projectID:  firstNonEmpty(cfg.ProjectID, getenv("AGENT_PLATFORM_PROJECT")),
		http:       httpClient,
		maxRetries: retries,
	}
}

// SetScope changes the default organisation/project for subsequent calls.
func (c *Client) SetScope(orgID, projectID string) {
	c.orgID = orgID
	c.projectID = projectID
}

// OrgID and ProjectID return the configured defaults.
func (c *Client) OrgID() string     { return c.orgID }
func (c *Client) ProjectID() string { return c.projectID }

// APIError is a non-2xx response.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
	Details    json.RawMessage
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api: status %d: %s: %s", e.StatusCode, e.Code, e.Message)
}

// IsNotFound reports a 404 API error.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// Health is the liveness document.
type Health struct {
	Status string `json:"status"`
}

// Meta is the version/environment document.
type Meta struct {
	Name        string `json:"name"`
	Environment string `json:"environment"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	BuildDate   string `json:"build_date"`
	Time        string `json:"time"`
}

func (c *Client) Health(ctx context.Context) (Health, error) {
	var out Health
	err := c.do(ctx, http.MethodGet, "/healthz", nil, nil, "", &out)
	return out, err
}

func (c *Client) Ready(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/readyz", nil, nil, "", nil)
}

func (c *Client) GetMeta(ctx context.Context) (Meta, error) {
	var out Meta
	err := c.do(ctx, http.MethodGet, "/api/v1/meta", nil, nil, "", &out)
	return out, err
}

// NewIdempotencyKey returns a random key suitable for execution submission.
func NewIdempotencyKey() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(raw)
}

func (c *Client) orgScope(orgID string) (string, error) {
	org := firstNonEmpty(orgID, c.orgID)
	if org == "" {
		return "", fmt.Errorf("client: org_id is required")
	}
	return "/api/v1/organizations/" + url.PathEscape(org), nil
}

func (c *Client) projectScope(orgID, projectID string) (string, error) {
	org := firstNonEmpty(orgID, c.orgID)
	if org == "" {
		return "", fmt.Errorf("client: org_id is required")
	}
	project := firstNonEmpty(projectID, c.projectID)
	if project == "" {
		return "", fmt.Errorf("client: project_id is required")
	}
	return "/api/v1/organizations/" + url.PathEscape(org) + "/projects/" + url.PathEscape(project), nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any,
	idempotencyKey string, out any) error {
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("api: encode request: %w", err)
		}
	}
	retryable := method == http.MethodGet || method == http.MethodHead || idempotencyKey != ""
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", "agent-platform-go-sdk/0.2.0")
		if len(payload) > 0 {
			request.Header.Set("Content-Type", "application/json")
		}
		if c.apiKey != "" {
			request.Header.Set("X-API-Key", c.apiKey)
		} else if c.token != "" {
			request.Header.Set("Authorization", "Bearer "+c.token)
		}
		if idempotencyKey != "" {
			request.Header.Set("Idempotency-Key", idempotencyKey)
		}
		response, err := c.http.Do(request)
		if err != nil {
			lastErr = fmt.Errorf("api: %s: %w", path, err)
			if !retryable || attempt == c.maxRetries || ctx.Err() != nil {
				return lastErr
			}
			sleepBackoff(ctx, attempt, 0)
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
		_ = response.Body.Close()
		if readErr != nil {
			return fmt.Errorf("api: read response: %w", readErr)
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			if out == nil || len(raw) == 0 {
				return nil
			}
			if err := json.Unmarshal(raw, out); err != nil {
				return fmt.Errorf("api: decode response: %w", err)
			}
			return nil
		}
		apiErr := decodeAPIError(response.StatusCode, raw)
		if response.StatusCode != http.StatusTooManyRequests &&
			response.StatusCode < 500 {
			return apiErr
		}
		lastErr = apiErr
		if !retryable || attempt == c.maxRetries {
			return apiErr
		}
		sleepBackoff(ctx, attempt, retryAfter(response.Header.Get("Retry-After")))
	}
	return lastErr
}

func decodeAPIError(status int, raw []byte) *APIError {
	apiErr := &APIError{StatusCode: status, Code: "api_error", Body: string(raw)}
	var envelope struct {
		Error struct {
			Code      string          `json:"code"`
			Message   string          `json:"message"`
			Details   json.RawMessage `json:"details"`
			RequestID string          `json:"request_id"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		apiErr.Code = firstNonEmpty(envelope.Error.Code, apiErr.Code)
		apiErr.Message = envelope.Error.Message
		apiErr.Details = envelope.Error.Details
		apiErr.RequestID = envelope.Error.RequestID
	}
	return apiErr
}

func retryAfter(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		return time.Until(when)
	}
	return 0
}

func sleepBackoff(ctx context.Context, attempt int, minimum time.Duration) {
	delay := minimum
	if delay <= 0 {
		delay = time.Duration(250*(1<<attempt)) * time.Millisecond
	}
	if delay > 5*time.Second {
		delay = 5 * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func getenv(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}
