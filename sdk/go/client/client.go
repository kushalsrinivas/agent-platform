// Package client is the Go SDK for the Agent Platform public API.
//
// It is intentionally thin: typed access to the documented REST surface, a consistent error type, and no
// retries. Retry policy belongs to the caller because only the caller knows whether an operation is idempotent.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client calls the platform API.
type Client struct {
	baseURL string
	http    *http.Client
}

// New builds a client for baseURL, for example http://localhost:8080.
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
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

// Health calls GET /healthz.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var out Health
	err := c.get(ctx, "/healthz", &out)
	return out, err
}

// Ready calls GET /readyz and fails when the API reports a dependency as unavailable.
func (c *Client) Ready(ctx context.Context) error {
	return c.get(ctx, "/readyz", nil)
}

// GetMeta calls GET /v1/meta.
func (c *Client) GetMeta(ctx context.Context) (Meta, error) {
	var out Meta
	err := c.get(ctx, "/v1/meta", &out)
	return out, err
}

// APIError is a non-2xx response. Body is included because platform errors are structured JSON.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api: status %d: %s", e.StatusCode, e.Body)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("api: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("api: decode %s: %w", path, err)
	}
	return nil
}
