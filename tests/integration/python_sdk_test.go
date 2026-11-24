//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func sdkPython(t *testing.T) string {
	t.Helper()
	if value := os.Getenv("SDK_PYTHON"); value != "" {
		return value
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "sdk", "python", ".venv", "bin", "python")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("SDK venv is missing (%v); run: cd sdk/python && uv sync", err)
	}
	return path
}

func TestPythonSDKLiveExitCriterion(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("python_sdk_" + suffix)
	pool := "python-sdk-" + suffix
	startRuntimeWorker(t, suffix, pool)
	org := client.request(http.MethodPost, "/v1/orgs", map[string]any{
		"name": "Python SDK Org", "slug": "python-sdk-" + suffix,
	}, nil, http.StatusCreated)
	orgID := asString(t, org, "id")
	admin := adminPool(t)
	t.Cleanup(func() { cleanupAPITestOrg(t, admin, orgID) })
	project := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects", map[string]any{
		"name": "Python SDK", "slug": "python-sdk",
	}, nil, http.StatusCreated)
	projectID := asString(t, project, "id")
	key := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/api-keys", map[string]any{
		"name": "python-sdk", "project_id": projectID, "role": "operator",
	}, nil, http.StatusCreated)

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "sdk", "python", "tests", "live_exit.py")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, sdkPython(t), script) //nolint:gosec // test-only path.
	command.Dir = filepath.Join(root, "sdk", "python")
	command.Env = append(os.Environ(),
		"AGENT_PLATFORM_URL="+client.base,
		"AGENT_PLATFORM_API_KEY="+asString(t, key, "key"),
		"AGENT_PLATFORM_ORG="+orgID,
		"AGENT_PLATFORM_PROJECT="+projectID,
		"AGENT_PLATFORM_POOL="+pool,
	)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf("python SDK exit script failed: %v\n%s", err, output.String())
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	var result struct {
		ExecutionID string `json:"execution_id"`
		State       string `json:"state"`
		Events      int    `json:"events"`
		Tools       int    `json:"tools"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &result); err != nil {
		t.Fatalf("decode SDK output %q: %v", output.String(), err)
	}
	if result.State != "succeeded" || result.Events == 0 || result.Tools == 0 {
		t.Fatalf("unexpected SDK result: %+v", result)
	}
}
