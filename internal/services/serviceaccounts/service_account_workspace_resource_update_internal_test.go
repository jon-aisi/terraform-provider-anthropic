// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

func TestServiceAccountWorkspaceSchema_WorkspaceRoleDoesNotRequireReplace(t *testing.T) {
	var schemaResp resource.SchemaResponse
	(&ServiceAccountWorkspaceResource{}).Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	attr, ok := schemaResp.Schema.Attributes["workspace_role"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("workspace_role is not a StringAttribute: %T", schemaResp.Schema.Attributes["workspace_role"])
	}
	if len(attr.PlanModifiers) != 0 {
		t.Errorf("workspace_role carries plan modifiers %v; a role change must be an in-place update", attr.PlanModifiers)
	}
}

func TestServiceAccountWorkspaceUpdate_UpsertsTheRole(t *testing.T) {
	ctx := context.Background()

	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Errorf("request body is not JSON: %v: %s", err, raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"service_account_id": "svac_01ABC",
			"workspace_id": "wrkspc_01XYZ",
			"workspace_role": "workspace_admin",
			"implicit": false,
			"created_by_actor_id": "user_01CREATOR",
			"type": "service_account_workspace_member"
		}`)
	}))
	t.Cleanup(srv.Close)

	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAuthToken("test"))
	r := &ServiceAccountWorkspaceResource{client: &providerdata.OAuthClient{Client: &c}}

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	stateModel := ServiceAccountWorkspaceResourceModel{
		ID:               types.StringValue("svac_01ABC:wrkspc_01XYZ"),
		ServiceAccountID: types.StringValue("svac_01ABC"),
		WorkspaceID:      types.StringValue("wrkspc_01XYZ"),
		WorkspaceRole:    types.StringValue("workspace_developer"),
		Implicit:         types.BoolValue(false),
		CreatedByActorID: types.StringValue("user_01CREATOR"),
	}
	planModel := stateModel
	planModel.WorkspaceRole = types.StringValue("workspace_admin")

	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if d := plan.Set(ctx, &planModel); d.HasError() {
		t.Fatalf("Plan.Set: %v", d)
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	if d := state.Set(ctx, &stateModel); d.HasError() {
		t.Fatalf("State.Set: %v", d)
	}

	resp := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/organizations/service_accounts/svac_01ABC/workspaces" {
		t.Errorf("request = %s %s, want POST /v1/organizations/service_accounts/svac_01ABC/workspaces", gotMethod, gotPath)
	}
	if gotBody["workspace_id"] != "wrkspc_01XYZ" || gotBody["workspace_role"] != "workspace_admin" {
		t.Errorf("body = %v, want workspace_id wrkspc_01XYZ and workspace_role workspace_admin", gotBody)
	}

	var role types.String
	if d := resp.State.GetAttribute(ctx, path.Root("workspace_role"), &role); d.HasError() {
		t.Fatalf("GetAttribute: %v", d)
	}
	if role.ValueString() != "workspace_admin" {
		t.Errorf("state workspace_role = %q, want workspace_admin", role.ValueString())
	}
	var id types.String
	if d := resp.State.GetAttribute(ctx, path.Root("id"), &id); d.HasError() {
		t.Fatalf("GetAttribute: %v", d)
	}
	if id.ValueString() != "svac_01ABC:wrkspc_01XYZ" {
		t.Errorf("state id = %q, want svac_01ABC:wrkspc_01XYZ", id.ValueString())
	}
}
