// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestFederationRuleSchema_ArchivedAttributesUseStateForUnknown(t *testing.T) {
	ctx := context.Background()
	var schemaResp resource.SchemaResponse
	(&FederationRuleResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	want := stringplanmodifier.UseStateForUnknown().Description(ctx)
	for _, name := range []string{"archived_at", "archived_by_actor_id"} {
		a, ok := schemaResp.Schema.Attributes[name].(schema.StringAttribute)
		if !ok {
			t.Fatalf("%s is not a StringAttribute: %T", name, schemaResp.Schema.Attributes[name])
		}
		found := false
		for _, m := range a.PlanModifiers {
			if m.Description(ctx) == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s lacks UseStateForUnknown; it never changes on an in-place update", name)
		}
	}

	// updated_* change on every update and issuer_name can change when the
	// issuer is renamed in the same apply: a carried-forward value would fail
	// Terraform's inconsistent-result check.
	for _, name := range []string{"updated_at", "updated_by_actor_id", "issuer_name"} {
		a := schemaResp.Schema.Attributes[name].(schema.StringAttribute)
		for _, m := range a.PlanModifiers {
			if m.Description(ctx) == want {
				t.Errorf("%s must not carry UseStateForUnknown", name)
			}
		}
	}
}

// workspaceIDsRequest builds a PlanModifyList request for workspace_ids from
// two models, with the planned list unknown as the framework marks Computed
// attributes on update.
func workspaceIDsRequest(t *testing.T, planModel, stateModel FederationRuleResourceModel) planmodifier.ListRequest {
	t.Helper()
	ctx := context.Background()
	var schemaResp resource.SchemaResponse
	(&FederationRuleResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	planModel.WorkspaceIDs = types.ListUnknown(types.StringType)
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if d := plan.Set(ctx, &planModel); d.HasError() {
		t.Fatalf("Plan.Set: %v", d)
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	if d := state.Set(ctx, &stateModel); d.HasError() {
		t.Fatalf("State.Set: %v", d)
	}
	return planmodifier.ListRequest{
		Path:        path.Root("workspace_ids"),
		Plan:        plan,
		State:       state,
		PlanValue:   planModel.WorkspaceIDs,
		StateValue:  stateModel.WorkspaceIDs,
		ConfigValue: types.ListNull(types.StringType),
	}
}

func TestWorkspaceIDsPlanModifier_UnchangedBindingKeepsState(t *testing.T) {
	state := ruleUpdateModel(t)
	plan := ruleUpdateModel(t)
	plan.TokenLifetimeSeconds = types.Int64Value(900)
	req := workspaceIDsRequest(t, plan, state)

	resp := planmodifier.ListResponse{PlanValue: req.PlanValue}
	workspaceIDsUseStateUnlessBindingChanges{}.PlanModifyList(context.Background(), req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if !resp.PlanValue.Equal(state.WorkspaceIDs) {
		t.Errorf("PlanValue = %v, want the prior state %v", resp.PlanValue, state.WorkspaceIDs)
	}
}

func TestWorkspaceIDsPlanModifier_WorkspaceIDChangeLeavesUnknown(t *testing.T) {
	state := ruleUpdateModel(t)
	plan := ruleUpdateModel(t)
	plan.WorkspaceID = types.StringValue("wrkspc_01NEW")
	req := workspaceIDsRequest(t, plan, state)

	resp := planmodifier.ListResponse{PlanValue: req.PlanValue}
	workspaceIDsUseStateUnlessBindingChanges{}.PlanModifyList(context.Background(), req, &resp)

	if !resp.PlanValue.IsUnknown() {
		t.Errorf("PlanValue = %v, want unknown: the API returns a new list after a binding change", resp.PlanValue)
	}
}

func TestWorkspaceIDsPlanModifier_AppliesToAllChangeLeavesUnknown(t *testing.T) {
	state := ruleUpdateModel(t)
	plan := ruleUpdateModel(t)
	plan.WorkspaceID = types.StringNull()
	plan.AppliesToAllWorkspaces = types.BoolValue(true)
	req := workspaceIDsRequest(t, plan, state)

	resp := planmodifier.ListResponse{PlanValue: req.PlanValue}
	workspaceIDsUseStateUnlessBindingChanges{}.PlanModifyList(context.Background(), req, &resp)

	if !resp.PlanValue.IsUnknown() {
		t.Errorf("PlanValue = %v, want unknown", resp.PlanValue)
	}
}

func TestWorkspaceIDsPlanModifier_UnknownWorkspaceIDLeavesUnknown(t *testing.T) {
	state := ruleUpdateModel(t)
	plan := ruleUpdateModel(t)
	plan.WorkspaceID = types.StringUnknown()
	req := workspaceIDsRequest(t, plan, state)

	resp := planmodifier.ListResponse{PlanValue: req.PlanValue}
	workspaceIDsUseStateUnlessBindingChanges{}.PlanModifyList(context.Background(), req, &resp)

	if !resp.PlanValue.IsUnknown() {
		t.Errorf("PlanValue = %v, want unknown: an unresolved workspace_id may still change the binding", resp.PlanValue)
	}
}

func TestWorkspaceIDsPlanModifier_CreateLeavesUnknown(t *testing.T) {
	plan := ruleUpdateModel(t)
	req := workspaceIDsRequest(t, plan, ruleUpdateModel(t))
	req.State = tfsdk.State{Raw: tftypes.NewValue(req.State.Raw.Type(), nil), Schema: req.State.Schema}
	req.StateValue = types.ListNull(types.StringType)

	resp := planmodifier.ListResponse{PlanValue: req.PlanValue}
	workspaceIDsUseStateUnlessBindingChanges{}.PlanModifyList(context.Background(), req, &resp)

	if !resp.PlanValue.IsUnknown() {
		t.Errorf("PlanValue = %v, want unknown on create", resp.PlanValue)
	}
}
