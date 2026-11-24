//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestOrgScopedAPIKeyAndLeastPrivilege(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("org_key_" + suffix)
	org := client.request(http.MethodPost, "/v1/orgs", map[string]any{
		"name": "Org Key", "slug": "org-key-" + suffix,
	}, nil, http.StatusCreated)
	orgID := asString(t, org, "id")
	admin := adminPool(t)
	t.Cleanup(func() { cleanupAPITestOrg(t, admin, orgID) })
	project := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects", map[string]any{
		"name": "Project", "slug": "project",
	}, nil, http.StatusCreated)
	projectID := asString(t, project, "id")
	key := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/api-keys", map[string]any{
		"name": "org-wide", "role": "operator",
	}, nil, http.StatusCreated)
	keyClient := newAPIClient(t)
	keyClient.token = ""
	headers := map[string]string{"X-API-Key": asString(t, key, "key")}

	keyClient.request(http.MethodGet, "/api/v1/organizations", nil, headers, http.StatusOK)
	keyClient.request(http.MethodGet, "/api/v1/organizations/"+orgID+"/projects/"+projectID, nil, headers, http.StatusOK)
	keyClient.request(http.MethodGet, "/api/v1/organizations/"+orgID+"/api-keys", nil, headers, http.StatusForbidden)
	keyClient.request(http.MethodPost, "/api/v1/organizations", map[string]any{
		"name": "Escalation", "slug": "escalation",
	}, headers, http.StatusForbidden)
}
