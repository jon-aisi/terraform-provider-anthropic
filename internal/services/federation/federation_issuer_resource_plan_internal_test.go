// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func issuerPlanModel() FederationIssuerResourceModel {
	return FederationIssuerResourceModel{
		ID:                    types.StringValue("fdis_01ABC"),
		Name:                  types.StringValue("github-actions"),
		IssuerURL:             types.StringValue("https://token.actions.githubusercontent.com"),
		JWKS:                  federationIssuerJWKSDiscoveryDefault,
		CheckJTI:              types.BoolValue(true),
		MaxJWTLifetimeSeconds: types.Int64Value(3600),
		JWKSPollingDisabledAt: types.StringNull(),
		CreatedAt:             types.StringValue("2026-01-01T00:00:00Z"),
		UpdatedAt:             types.StringValue("2026-01-02T00:00:00Z"),
		ArchivedAt:            types.StringNull(),
		CreatedByActorID:      types.StringValue("user_01actor"),
		UpdatedByActorID:      types.StringValue("user_01actor"),
		ArchivedByActorID:     types.StringNull(),
	}
}

var explicitURLJWKS = types.ObjectValueMust(federationIssuerJWKSAttrTypes, map[string]attr.Value{
	"type":           types.StringValue("explicit_url"),
	"discovery_base": types.StringNull(),
	"url":            types.StringValue("https://keys.example.com/jwks.json"),
	"keys":           jsontypes.NewNormalizedNull(),
	"ca_cert_pem":    types.StringNull(),
})

// warningPaths returns the attribute path of every warning, as strings.
func warningPaths(t *testing.T, diags diag.Diagnostics) []string {
	t.Helper()
	var paths []string
	for _, d := range diags.Warnings() {
		withPath, ok := d.(diag.DiagnosticWithPath)
		if !ok {
			t.Fatalf("warning %q carries no attribute path", d.Summary())
		}
		paths = append(paths, withPath.Path().String())
	}
	return paths
}

func TestFederationIssuerTrustChangeWarnings_IssuerURL(t *testing.T) {
	state := issuerPlanModel()
	plan := issuerPlanModel()
	plan.IssuerURL = types.StringValue("https://evil.example.com")

	diags := federationIssuerTrustChangeWarnings(plan, state)

	if got := warningPaths(t, diags); len(got) != 1 || got[0] != path.Root("issuer_url").String() {
		t.Fatalf("warning paths = %v, want [issuer_url]", got)
	}
	detail := diags.Warnings()[0].Detail()
	for _, want := range []string{"github-actions", "https://token.actions.githubusercontent.com", "https://evil.example.com", "every federation rule"} {
		if !strings.Contains(strings.ToLower(detail), strings.ToLower(want)) {
			t.Errorf("detail = %q, want it to contain %q", detail, want)
		}
	}
}

func TestFederationIssuerTrustChangeWarnings_JWKS(t *testing.T) {
	state := issuerPlanModel()
	plan := issuerPlanModel()
	plan.JWKS = explicitURLJWKS

	diags := federationIssuerTrustChangeWarnings(plan, state)

	if got := warningPaths(t, diags); len(got) != 1 || got[0] != path.Root("jwks").String() {
		t.Fatalf("warning paths = %v, want [jwks]", got)
	}
}

func TestFederationIssuerTrustChangeWarnings_RenameAndLifetimeAreQuiet(t *testing.T) {
	state := issuerPlanModel()
	plan := issuerPlanModel()
	plan.Name = types.StringValue("github-actions-renamed")
	plan.MaxJWTLifetimeSeconds = types.Int64Value(900)
	plan.CheckJTI = types.BoolValue(false)

	if diags := federationIssuerTrustChangeWarnings(plan, state); diags.WarningsCount() != 0 {
		t.Fatalf("expected no warning for a rename or lifetime change, got %v", diags)
	}
}

func TestFederationIssuerTrustChangeWarnings_UnknownIssuerURLIsQuiet(t *testing.T) {
	state := issuerPlanModel()
	plan := issuerPlanModel()
	plan.IssuerURL = types.StringUnknown()

	if diags := federationIssuerTrustChangeWarnings(plan, state); diags.WarningsCount() != 0 {
		t.Fatalf("an unresolved issuer_url cannot be reviewed in the plan and must not warn, got %v", diags)
	}
}

// issuerPlanAndState marshals two models into a tfsdk.Plan and tfsdk.State
// for the resource schema.
func issuerPlanAndState(t *testing.T, planModel, stateModel FederationIssuerResourceModel) (tfsdk.Plan, tfsdk.State) {
	t.Helper()
	ctx := context.Background()
	var schemaResp resource.SchemaResponse
	(&FederationIssuerResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if d := plan.Set(ctx, &planModel); d.HasError() {
		t.Fatalf("Plan.Set: %v", d)
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	if d := state.Set(ctx, &stateModel); d.HasError() {
		t.Fatalf("State.Set: %v", d)
	}
	return plan, state
}

func TestFederationIssuerModifyPlan_UpdateWarnsOnIssuerURL(t *testing.T) {
	ctx := context.Background()
	stateModel := issuerPlanModel()
	planModel := issuerPlanModel()
	planModel.IssuerURL = types.StringValue("https://other-idp.example.com")
	plan, state := issuerPlanAndState(t, planModel, stateModel)

	var resp resource.ModifyPlanResponse
	resp.Plan = plan
	(&FederationIssuerResource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected errors: %v", resp.Diagnostics)
	}
	if got := warningPaths(t, resp.Diagnostics); len(got) != 1 || got[0] != path.Root("issuer_url").String() {
		t.Fatalf("warning paths = %v, want [issuer_url]", got)
	}
	// ModifyPlan warns; it does not alter the plan.
	if !resp.Plan.Raw.Equal(plan.Raw) {
		t.Error("ModifyPlan changed the planned value")
	}
}

func TestFederationIssuerModifyPlan_CreateAndDestroyAreQuiet(t *testing.T) {
	ctx := context.Background()
	plan, state := issuerPlanAndState(t, issuerPlanModel(), issuerPlanModel())
	nullRaw := tftypes.NewValue(plan.Raw.Type(), nil)

	var createResp resource.ModifyPlanResponse
	createResp.Plan = plan
	(&FederationIssuerResource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: tfsdk.State{Raw: nullRaw, Schema: state.Schema}}, &createResp)
	if createResp.Diagnostics.WarningsCount() != 0 || createResp.Diagnostics.HasError() {
		t.Errorf("create: unexpected diagnostics %v", createResp.Diagnostics)
	}

	var destroyResp resource.ModifyPlanResponse
	destroyResp.Plan = tfsdk.Plan{Raw: nullRaw, Schema: plan.Schema}
	(&FederationIssuerResource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: tfsdk.Plan{Raw: nullRaw, Schema: plan.Schema}, State: state}, &destroyResp)
	if destroyResp.Diagnostics.WarningsCount() != 0 || destroyResp.Diagnostics.HasError() {
		t.Errorf("destroy: unexpected diagnostics %v", destroyResp.Diagnostics)
	}
}
