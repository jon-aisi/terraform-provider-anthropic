// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ruleUpdateModel is a fully populated rule as Terraform would hold it after
// a Read: one workspace binding, no extra enablements.
func ruleUpdateModel(t *testing.T) FederationRuleResourceModel {
	t.Helper()
	return FederationRuleResourceModel{
		ID:                     types.StringValue("fdrl_01ABC"),
		Name:                   types.StringValue("gha-deploy"),
		Description:            types.StringNull(),
		IssuerID:               types.StringValue("fdis_01XYZ"),
		Match:                  matchObject(t, types.StringValue("repo:my-org/*"), types.StringNull(), types.StringNull(), types.MapNull(types.StringType)),
		Target:                 targetObject(t, "svac_01SVC"),
		OAuthScope:             types.StringValue("workspace:developer"),
		WorkspaceID:            types.StringValue("wrkspc_01OLD"),
		AppliesToAllWorkspaces: types.BoolValue(false),
		TokenLifetimeSeconds:   types.Int64Value(3600),
		Attributes:             types.MapNull(types.StringType),
		WorkspaceIDs:           types.ListValueMust(types.StringType, []attr.Value{types.StringValue("wrkspc_01OLD")}),
	}
}

// sendRuleUpdate builds the update body from plan and state, sends it to an
// httptest server and returns the decoded JSON body the API received.
func sendRuleUpdate(t *testing.T, plan, state FederationRuleResourceModel) map[string]any {
	t.Helper()
	ctx := context.Background()

	var gotBody map[string]any
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Errorf("request body is not JSON: %v: %s", err, raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, federationRuleJSON("null"))
	}))
	t.Cleanup(srv.Close)

	params, diags := buildFederationRuleUpdateParams(ctx, plan, state)
	if diags.HasError() {
		t.Fatalf("buildFederationRuleUpdateParams: %v", diags)
	}
	client := newTestOAuthClient(srv)
	if _, err := client.Beta.Organization.Federation.Rules.Update(ctx, state.ID.ValueString(), params); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/organizations/federation_rules/fdrl_01ABC" {
		t.Errorf("request = %s %s, want POST /v1/organizations/federation_rules/fdrl_01ABC", gotMethod, gotPath)
	}
	return gotBody
}

func TestFederationRuleUpdateBody_LifetimeChangeOmitsWorkspaceID(t *testing.T) {
	state := ruleUpdateModel(t)
	plan := ruleUpdateModel(t)
	plan.TokenLifetimeSeconds = types.Int64Value(7200)

	body := sendRuleUpdate(t, plan, state)

	if _, present := body["workspace_id"]; present {
		t.Errorf("workspace_id was sent although unchanged: %v", body["workspace_id"])
	}
	if got := body["token_lifetime_seconds"]; got != float64(7200) {
		t.Errorf("token_lifetime_seconds = %v, want 7200", got)
	}
	if got := body["applies_to_all_workspaces"]; got != false {
		t.Errorf("applies_to_all_workspaces = %v, want false", got)
	}
	for _, key := range []string{"name", "oauth_scope", "match", "target"} {
		if _, present := body[key]; !present {
			t.Errorf("%s missing from the update body: %v", key, body)
		}
	}
}

func TestFederationRuleUpdateBody_WorkspaceChangeSendsNewWorkspaceID(t *testing.T) {
	state := ruleUpdateModel(t)
	plan := ruleUpdateModel(t)
	plan.WorkspaceID = types.StringValue("wrkspc_01NEW")

	body := sendRuleUpdate(t, plan, state)

	if got := body["workspace_id"]; got != "wrkspc_01NEW" {
		t.Errorf("workspace_id = %v, want wrkspc_01NEW", got)
	}
}

func TestFederationRuleUpdateBody_ClearedWorkspaceIDIsSentAsNull(t *testing.T) {
	state := ruleUpdateModel(t)
	plan := ruleUpdateModel(t)
	plan.WorkspaceID = types.StringNull()
	plan.AppliesToAllWorkspaces = types.BoolValue(true)

	body := sendRuleUpdate(t, plan, state)

	got, present := body["workspace_id"]
	if !present {
		t.Fatalf("workspace_id missing: the legacy binding would survive server-side: %v", body)
	}
	if got != nil {
		t.Errorf("workspace_id = %v, want null", got)
	}
	if body["applies_to_all_workspaces"] != true {
		t.Errorf("applies_to_all_workspaces = %v, want true", body["applies_to_all_workspaces"])
	}
}

func TestFederationRuleUpdateBody_AllWorkspacesBackToOneSendsWorkspaceID(t *testing.T) {
	state := ruleUpdateModel(t)
	state.WorkspaceID = types.StringNull()
	state.AppliesToAllWorkspaces = types.BoolValue(true)
	// With applies_to_all_workspaces the API lists every workspace here; that
	// is not a per-workspace enablement and must not block the switch back.
	state.WorkspaceIDs = types.ListValueMust(types.StringType, []attr.Value{types.StringValue("wrkspc_01A"), types.StringValue("wrkspc_01B")})
	plan := ruleUpdateModel(t)
	plan.WorkspaceID = types.StringValue("wrkspc_01A")
	plan.AppliesToAllWorkspaces = types.BoolValue(false)

	body := sendRuleUpdate(t, plan, state)

	if got := body["workspace_id"]; got != "wrkspc_01A" {
		t.Errorf("workspace_id = %v, want wrkspc_01A", got)
	}
	if body["applies_to_all_workspaces"] != false {
		t.Errorf("applies_to_all_workspaces = %v, want false", body["applies_to_all_workspaces"])
	}
}

func TestFederationRuleUpdateBody_WorkspaceChangeWithExtraEnablementsIsAnError(t *testing.T) {
	state := ruleUpdateModel(t)
	state.WorkspaceIDs = types.ListValueMust(types.StringType, []attr.Value{types.StringValue("wrkspc_01OLD"), types.StringValue("wrkspc_01EXTRA")})
	plan := ruleUpdateModel(t)
	plan.WorkspaceID = types.StringValue("wrkspc_01NEW")

	_, diags := buildFederationRuleUpdateParams(context.Background(), plan, state)
	if !diags.HasError() {
		t.Fatal("expected an error: the API rejects workspace_id on a rule enabled for more than one workspace")
	}
	if !strings.Contains(diags.Errors()[0].Detail(), "anthropic_federation_rule_workspace") {
		t.Errorf("detail = %q, want it to point at the enablement resource", diags.Errors()[0].Detail())
	}
}

func TestFederationRuleUpdateBody_UnchangedWorkspaceWithExtraEnablementsIsAccepted(t *testing.T) {
	// The finding this guards against: a rule with enablements could not be
	// updated at all, because every update resent workspace_id.
	state := ruleUpdateModel(t)
	state.WorkspaceIDs = types.ListValueMust(types.StringType, []attr.Value{types.StringValue("wrkspc_01OLD"), types.StringValue("wrkspc_01EXTRA")})
	plan := ruleUpdateModel(t)
	plan.WorkspaceIDs = state.WorkspaceIDs
	plan.TokenLifetimeSeconds = types.Int64Value(900)

	body := sendRuleUpdate(t, plan, state)

	if _, present := body["workspace_id"]; present {
		t.Errorf("workspace_id was sent although unchanged: %v", body["workspace_id"])
	}
	if got := body["token_lifetime_seconds"]; got != float64(900) {
		t.Errorf("token_lifetime_seconds = %v, want 900", got)
	}
}
