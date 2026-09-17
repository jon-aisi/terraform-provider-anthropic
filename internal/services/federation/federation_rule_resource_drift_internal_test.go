// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// federationRuleJSON is a GET response body; archivedAt is the raw JSON for
// the archived_at field (`null` or a quoted RFC 3339 timestamp).
func federationRuleJSON(archivedAt string) string {
	return `{
		"id": "fdrl_01ABC",
		"applies_to_all_workspaces": false,
		"archived_at": ` + archivedAt + `,
		"archived_by_actor_id": null,
		"attributes": null,
		"created_at": "2024-01-15T10:00:00Z",
		"created_by_actor_id": "user_01CREATOR",
		"description": "",
		"issuer_id": "fdis_01XYZ",
		"issuer_name": "GitHub Actions",
		"match": {"subject_prefix": "repo:my-org/*"},
		"name": "gha-deploy",
		"oauth_scope": "workspace:developer",
		"target": {"service_account_id": "svac_01SVC", "type": "service_account"},
		"token_lifetime_seconds": 3600,
		"type": "federation_rule",
		"updated_at": "2024-01-15T11:00:00Z",
		"updated_by_actor_id": "user_01UPDATER",
		"workspace_id": "wrkspc_01ABC",
		"workspace_ids": ["wrkspc_01ABC"]
	}`
}

// readFederationRule runs Read against a server answering GET with body,
// starting from a state that holds only the id.
func readFederationRule(t *testing.T, body string) resource.ReadResponse {
	t.Helper()
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/federation_rules/fdrl_01ABC" {
			http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	r := &FederationRuleResource{client: newTestOAuthClient(srv)}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	vals := nullValuesForSchema(t)
	vals["id"] = tftypes.NewValue(tftypes.String, "fdrl_01ABC")
	state := tfsdk.State{Raw: tftypes.NewValue(schemaType(t), vals), Schema: schemaResp.Schema}

	resp := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)
	return resp
}

func TestFederationRuleRead_ArchivedOutsideTerraformWarnsAndKeepsState(t *testing.T) {
	resp := readFederationRule(t, federationRuleJSON(`"2024-06-01T12:00:00Z"`))

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected errors: %v", resp.Diagnostics)
	}
	if got := resp.Diagnostics.WarningsCount(); got != 1 {
		t.Fatalf("WarningsCount = %d, want 1: %v", got, resp.Diagnostics)
	}
	warning := resp.Diagnostics.Warnings()[0]
	if !strings.Contains(warning.Summary(), "archived outside Terraform") {
		t.Errorf("summary = %q, want it to name the out-of-band archive", warning.Summary())
	}
	for _, want := range []string{"gha-deploy", "fdrl_01ABC", "2024-06-01T12:00:00Z", "remove it from configuration"} {
		if !strings.Contains(warning.Detail(), want) {
			t.Errorf("detail = %q, want it to contain %q", warning.Detail(), want)
		}
	}

	// The rule must stay in state: RemoveResource would plan a re-create,
	// re-granting access revoked in the Console.
	if resp.State.Raw.IsNull() {
		t.Fatal("archived rule was removed from state")
	}
	var archivedAt types.String
	if d := resp.State.GetAttribute(context.Background(), path.Root("archived_at"), &archivedAt); d.HasError() {
		t.Fatalf("GetAttribute: %v", d)
	}
	if archivedAt.ValueString() != "2024-06-01T12:00:00Z" {
		t.Errorf("archived_at = %q, want 2024-06-01T12:00:00Z", archivedAt.ValueString())
	}
}

func TestFederationRuleRead_LiveRuleHasNoWarning(t *testing.T) {
	resp := readFederationRule(t, federationRuleJSON("null"))

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected errors: %v", resp.Diagnostics)
	}
	if got := resp.Diagnostics.WarningsCount(); got != 0 {
		t.Fatalf("WarningsCount = %d, want 0: %v", got, resp.Diagnostics)
	}
	var archivedAt types.String
	if d := resp.State.GetAttribute(context.Background(), path.Root("archived_at"), &archivedAt); d.HasError() {
		t.Fatalf("GetAttribute: %v", d)
	}
	if !archivedAt.IsNull() {
		t.Errorf("archived_at = %q, want null", archivedAt.ValueString())
	}
}
