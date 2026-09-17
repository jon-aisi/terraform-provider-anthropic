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

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

func TestFederationIssuerRead_ErrorBodyIsTruncatedInTheDiagnostic(t *testing.T) {
	t.Parallel()

	body := `{"type":"error","error":{"type":"api_error","message":"` + strings.Repeat("x", 2048) + `"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("request-id", "req_123")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	ctx := context.Background()
	sdk := anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(srv.URL),
		option.WithAuthToken("test"),
		option.WithMaxRetries(0),
	)
	r := &FederationIssuerResource{client: &providerdata.OAuthClient{Client: &sdk}}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	vals := federationIssuerNullValues(t)
	vals["id"] = tftypes.NewValue(tftypes.String, "fdis_01ABC")
	state := tfsdk.State{Raw: tftypes.NewValue(federationIssuerSchemaType(t), vals), Schema: schemaResp.Schema}

	resp := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)

	if got := resp.Diagnostics.ErrorsCount(); got != 1 {
		t.Fatalf("ErrorsCount = %d, want 1: %v", got, resp.Diagnostics)
	}
	detail := resp.Diagnostics.Errors()[0].Detail()
	for _, want := range []string{
		"Unable to read federation issuer: ",
		"500 Internal Server Error",
		"Request-ID: req_123",
		body[:admin.MaxErrorBodyBytes] + "... [",
		"more bytes truncated]",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail = %q\n does not contain %q", detail, want)
		}
	}
	if strings.Contains(detail, body) {
		t.Error("the diagnostic carries the whole response body")
	}
	if len(detail) >= len(body) {
		t.Errorf("detail is %d bytes for a %d-byte body; want it shorter than the body", len(detail), len(body))
	}
}
