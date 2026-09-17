// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func ruleAccessWarnings(t *testing.T, plan, state FederationRuleResourceModel) []string {
	t.Helper()
	return warningPaths(t, federationRuleAccessChangeWarnings(context.Background(), plan, state))
}

func TestFederationRuleAccessChangeWarnings_Match(t *testing.T) {
	state := ruleUpdateModel(t)
	plan := ruleUpdateModel(t)
	plan.Match = matchObject(t, types.StringValue("repo:other-org/*"), types.StringNull(), types.StringNull(), types.MapNull(types.StringType))

	if got := ruleAccessWarnings(t, plan, state); len(got) != 1 || got[0] != path.Root("match").String() {
		t.Fatalf("warning paths = %v, want [match]", got)
	}
}

func TestFederationRuleAccessChangeWarnings_Target(t *testing.T) {
	state := ruleUpdateModel(t)
	plan := ruleUpdateModel(t)
	// The Computed service_account_name is unknown whenever target changes.
	plan.Target = types.ObjectValueMust(federationRuleTargetAttrTypes, map[string]attr.Value{
		"service_account_id":   types.StringValue("svac_01OTHER"),
		"service_account_name": types.StringUnknown(),
	})

	diags := federationRuleAccessChangeWarnings(context.Background(), plan, state)
	if got := warningPaths(t, diags); len(got) != 1 || got[0] != path.Root("target").AtName("service_account_id").String() {
		t.Fatalf("warning paths = %v, want [target.service_account_id]", got)
	}
	detail := diags.Warnings()[0].Detail()
	if !strings.Contains(detail, "svac_01OTHER") || !strings.Contains(detail, "svac_01SVC") {
		t.Errorf("detail = %q, want both service account ids", detail)
	}
}

func TestFederationRuleAccessChangeWarnings_Scope(t *testing.T) {
	state := ruleUpdateModel(t)
	plan := ruleUpdateModel(t)
	plan.OAuthScope = types.StringValue("workspace:inference")

	if got := ruleAccessWarnings(t, plan, state); len(got) != 1 || got[0] != path.Root("oauth_scope").String() {
		t.Fatalf("warning paths = %v, want [oauth_scope]", got)
	}
}

func TestFederationRuleAccessChangeWarnings_WorkspaceID(t *testing.T) {
	state := ruleUpdateModel(t)
	plan := ruleUpdateModel(t)
	plan.WorkspaceID = types.StringValue("wrkspc_01NEW")

	if got := ruleAccessWarnings(t, plan, state); len(got) != 1 || got[0] != path.Root("workspace_id").String() {
		t.Fatalf("warning paths = %v, want [workspace_id]", got)
	}
}

func TestFederationRuleAccessChangeWarnings_AppliesToAllWorkspaces(t *testing.T) {
	state := ruleUpdateModel(t)
	plan := ruleUpdateModel(t)
	plan.WorkspaceID = types.StringNull()
	plan.AppliesToAllWorkspaces = types.BoolValue(true)

	diags := federationRuleAccessChangeWarnings(context.Background(), plan, state)
	if got := warningPaths(t, diags); len(got) != 1 || got[0] != path.Root("applies_to_all_workspaces").String() {
		t.Fatalf("warning paths = %v, want one warning at applies_to_all_workspaces", got)
	}
	if detail := diags.Warnings()[0].Detail(); !strings.Contains(detail, "wrkspc_01OLD -> null") || !strings.Contains(detail, "false -> true") {
		t.Errorf("detail = %q, want the old and new binding", detail)
	}
}

func TestFederationRuleAccessChangeWarnings_DescriptionAndLifetimeAreQuiet(t *testing.T) {
	state := ruleUpdateModel(t)
	plan := ruleUpdateModel(t)
	plan.Name = types.StringValue("gha-deploy-renamed")
	plan.Description = types.StringValue("new description")
	plan.TokenLifetimeSeconds = types.Int64Value(900)

	if got := ruleAccessWarnings(t, plan, state); len(got) != 0 {
		t.Fatalf("expected no warning for name, description or lifetime changes, got %v", got)
	}
}

func TestFederationRuleAccessChangeWarnings_UnknownValuesAreQuiet(t *testing.T) {
	state := ruleUpdateModel(t)
	plan := ruleUpdateModel(t)
	plan.Target = types.ObjectUnknown(federationRuleTargetAttrTypes)
	plan.OAuthScope = types.StringUnknown()
	plan.WorkspaceID = types.StringUnknown()

	if got := ruleAccessWarnings(t, plan, state); len(got) != 0 {
		t.Fatalf("unresolved values cannot be reviewed in the plan and must not warn, got %v", got)
	}
}

func TestFederationRuleModifyPlan_UpdateWarnsOnScope(t *testing.T) {
	ctx := context.Background()
	var schemaResp resource.SchemaResponse
	(&FederationRuleResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	stateModel := ruleUpdateModel(t)
	planModel := ruleUpdateModel(t)
	planModel.OAuthScope = types.StringValue("workspace:inference")

	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if d := plan.Set(ctx, &planModel); d.HasError() {
		t.Fatalf("Plan.Set: %v", d)
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	if d := state.Set(ctx, &stateModel); d.HasError() {
		t.Fatalf("State.Set: %v", d)
	}

	var resp resource.ModifyPlanResponse
	resp.Plan = plan
	(&FederationRuleResource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected errors: %v", resp.Diagnostics)
	}
	if got := warningPaths(t, resp.Diagnostics); len(got) != 1 || got[0] != path.Root("oauth_scope").String() {
		t.Fatalf("warning paths = %v, want [oauth_scope]", got)
	}
	if !resp.Plan.Raw.Equal(plan.Raw) {
		t.Error("ModifyPlan changed the planned value")
	}

	// Create: no prior state, nothing to compare.
	var createResp resource.ModifyPlanResponse
	createResp.Plan = plan
	(&FederationRuleResource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: tfsdk.State{Raw: tftypes.NewValue(plan.Raw.Type(), nil), Schema: schemaResp.Schema}}, &createResp)
	if createResp.Diagnostics.WarningsCount() != 0 || createResp.Diagnostics.HasError() {
		t.Errorf("create: unexpected diagnostics %v", createResp.Diagnostics)
	}
}
