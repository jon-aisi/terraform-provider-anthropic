// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

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

// serviceAccountJSON is a GET response body; archivedAt is the raw JSON for
// the archived_at field (`null` or a quoted RFC 3339 timestamp).
func serviceAccountJSON(archivedAt string) string {
	return `{
		"id": "svac_01ABC",
		"name": "ci-runner",
		"description": "",
		"organization_role": "developer",
		"created_at": "2024-01-15T10:00:00Z",
		"updated_at": "2024-01-15T10:00:00Z",
		"archived_at": ` + archivedAt + `,
		"created_by_actor_id": "user_01ABC",
		"updated_by_actor_id": "user_01ABC",
		"archived_by_actor_id": null,
		"type": "service_account"
	}`
}

// readServiceAccount runs Read against a server answering GET with body,
// starting from a state that holds only the id.
func readServiceAccount(t *testing.T, body string) resource.ReadResponse {
	t.Helper()
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/service_accounts/svac_01ABC" {
			http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	r := &ServiceAccountResource{client: newTestServiceAccountOAuthClient(t, srv)}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	schemaObjType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	vals := make(map[string]tftypes.Value, len(schemaObjType.AttributeTypes))
	for name, typ := range schemaObjType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	vals["id"] = tftypes.NewValue(tftypes.String, "svac_01ABC")
	state := tfsdk.State{Raw: tftypes.NewValue(schemaObjType, vals), Schema: schemaResp.Schema}

	resp := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)
	return resp
}

func TestServiceAccountRead_ArchivedOutsideTerraformWarnsAndKeepsState(t *testing.T) {
	resp := readServiceAccount(t, serviceAccountJSON(`"2024-06-01T12:00:00Z"`))

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
	for _, want := range []string{"ci-runner", "svac_01ABC", "2024-06-01T12:00:00Z"} {
		if !strings.Contains(warning.Detail(), want) {
			t.Errorf("detail = %q, want it to contain %q", warning.Detail(), want)
		}
	}

	if resp.State.Raw.IsNull() {
		t.Fatal("archived service account was removed from state")
	}
	var archivedAt types.String
	if d := resp.State.GetAttribute(context.Background(), path.Root("archived_at"), &archivedAt); d.HasError() {
		t.Fatalf("GetAttribute: %v", d)
	}
	if archivedAt.ValueString() != "2024-06-01T12:00:00Z" {
		t.Errorf("archived_at = %q, want 2024-06-01T12:00:00Z", archivedAt.ValueString())
	}
}

func TestServiceAccountRead_LiveServiceAccountHasNoWarning(t *testing.T) {
	resp := readServiceAccount(t, serviceAccountJSON("null"))

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected errors: %v", resp.Diagnostics)
	}
	if got := resp.Diagnostics.WarningsCount(); got != 0 {
		t.Fatalf("WarningsCount = %d, want 0: %v", got, resp.Diagnostics)
	}
}
