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

// readFederationIssuer runs Read against a server answering GET with body,
// starting from a state that holds only the id.
func readFederationIssuer(t *testing.T, body string) resource.ReadResponse {
	t.Helper()
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/federation_issuers/fdis_01ABC" {
			http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	r := &FederationIssuerResource{client: newTestFederationClient(t, srv)}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	schemaObjType := federationIssuerSchemaType(t).(tftypes.Object)
	vals := make(map[string]tftypes.Value, len(schemaObjType.AttributeTypes))
	for name, typ := range schemaObjType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	vals["id"] = tftypes.NewValue(tftypes.String, "fdis_01ABC")
	state := tfsdk.State{Raw: tftypes.NewValue(schemaObjType, vals), Schema: schemaResp.Schema}

	resp := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)
	return resp
}

func TestFederationIssuerRead_ArchivedOutsideTerraformWarnsAndKeepsState(t *testing.T) {
	body := strings.Replace(federationIssuerFixture(`{"type": "discovery"}`), `"archived_at": null`, `"archived_at": "2026-03-01T09:00:00Z"`, 1)
	resp := readFederationIssuer(t, body)

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
	for _, want := range []string{"github-actions", "fdis_01ABC", "2026-03-01T09:00:00Z"} {
		if !strings.Contains(warning.Detail(), want) {
			t.Errorf("detail = %q, want it to contain %q", warning.Detail(), want)
		}
	}

	if resp.State.Raw.IsNull() {
		t.Fatal("archived issuer was removed from state")
	}
	var archivedAt types.String
	if d := resp.State.GetAttribute(context.Background(), path.Root("archived_at"), &archivedAt); d.HasError() {
		t.Fatalf("GetAttribute: %v", d)
	}
	if archivedAt.ValueString() != "2026-03-01T09:00:00Z" {
		t.Errorf("archived_at = %q, want 2026-03-01T09:00:00Z", archivedAt.ValueString())
	}
}

func TestFederationIssuerRead_LiveIssuerHasNoWarning(t *testing.T) {
	resp := readFederationIssuer(t, federationIssuerFixture(`{"type": "discovery"}`))

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected errors: %v", resp.Diagnostics)
	}
	if got := resp.Diagnostics.WarningsCount(); got != 0 {
		t.Fatalf("WarningsCount = %d, want 0: %v", got, resp.Diagnostics)
	}
}
