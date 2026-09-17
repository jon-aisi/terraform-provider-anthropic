// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const (
	testRuleID      = "fdrl_01TESTRULE"
	testOrgID       = "0f1e2d3c-4b5a-4968-8778-695a4b3c2d1e"
	testServiceAcct = "svac_01TESTSA"
	testWorkspace   = "wrkspc_01TESTWS"
)

// exchange is one request the fake token endpoint received.
type exchange struct {
	header http.Header
	body   map[string]any
}

// fakeAPI is a TLS server standing in for api.anthropic.com: it mints an
// access token at POST /v1/oauth/token and records the headers of every other
// request, which it answers with `{}`.
type fakeAPI struct {
	*httptest.Server

	// expiresIn is the lifetime reported for every minted token.
	expiresIn int

	mu        sync.Mutex
	exchanges []exchange
	apiCalls  []http.Header
}

func newFakeAPI(t *testing.T, expiresIn int) *fakeAPI {
	t.Helper()
	f := &fakeAPI{expiresIn: expiresIn}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		if r.Method == http.MethodPost && r.URL.Path == "/v1/oauth/token" {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			f.exchanges = append(f.exchanges, exchange{header: r.Header.Clone(), body: body})
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":"at-%d","token_type":"Bearer","expires_in":%d}`, len(f.exchanges), f.expiresIn)
			return
		}

		f.apiCalls = append(f.apiCalls, r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAPI) snapshot() (exchanges []exchange, apiCalls []http.Header) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]exchange(nil), f.exchanges...), append([]http.Header(nil), f.apiCalls...)
}

// writeIdentityToken writes token to a file under the test's temp dir and
// returns its path.
func writeIdentityToken(t *testing.T, token string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "identity-token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("write identity token: %v", err)
	}
	return path
}

// federationAttrs is a complete federation configuration through provider
// arguments, with the given identity token file.
func federationAttrs(tokenFile string) map[string]tftypes.Value {
	return map[string]tftypes.Value{
		"identity_token_file": str(tokenFile),
		"federation_rule_id":  str(testRuleID),
		"organization_id":     str(testOrgID),
		"service_account_id":  str(testServiceAcct),
		"workspace_id":        str(testWorkspace),
	}
}

func TestFederationExchangesIdentityTokenForBearer(t *testing.T) {
	api := newFakeAPI(t, 3600)
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", api.URL)
	tokenFile := writeIdentityToken(t, "jwt-A")

	resp := configureProviderWith(t, api.Client(), federationAttrs(tokenFile))
	pd := providerDataFrom(t, resp)
	if resp.Diagnostics.WarningsCount() != 0 {
		t.Errorf("unexpected warnings: %v", resp.Diagnostics)
	}
	if pd.AdminClient != nil {
		t.Error("AdminClient should be nil without admin_api_key")
	}

	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}

	exchanges, apiCalls := api.snapshot()
	if len(exchanges) != 1 {
		t.Fatalf("token exchanges = %d, want 1", len(exchanges))
	}
	ex := exchanges[0]

	// The exchange itself carries no credential header: the assertion is the credential.
	for _, h := range []string{"Authorization", "X-Api-Key"} {
		if v := ex.header.Get(h); v != "" {
			t.Errorf("token exchange sent %s: %q", h, v)
		}
	}
	if ct := ex.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("token exchange content-type = %q, want application/json", ct)
	}
	if beta := ex.header.Get("anthropic-beta"); !strings.Contains(beta, "oidc-federation") {
		t.Errorf("token exchange anthropic-beta = %q, want the federation beta", beta)
	}

	want := map[string]any{
		"grant_type":         "urn:ietf:params:oauth:grant-type:jwt-bearer",
		"assertion":          "jwt-A",
		"federation_rule_id": testRuleID,
		"organization_id":    testOrgID,
		"service_account_id": testServiceAcct,
		"workspace_id":       testWorkspace,
	}
	for k, v := range want {
		if ex.body[k] != v {
			t.Errorf("exchange body %s = %v, want %v", k, ex.body[k], v)
		}
	}
	if len(ex.body) != len(want) {
		t.Errorf("exchange body has %d fields, want %d: %v", len(ex.body), len(want), ex.body)
	}

	if len(apiCalls) != 1 {
		t.Fatalf("API calls = %d, want 1", len(apiCalls))
	}
	if got := apiCalls[0].Get("Authorization"); got != "Bearer at-1" {
		t.Errorf("API authorization = %q, want the minted bearer", got)
	}
	if v := apiCalls[0].Get("X-Api-Key"); v != "" {
		t.Errorf("API call also sent x-api-key: %q", v)
	}
}

// TestFederationRereadsTheTokenFileOnEveryExchange: an identity token's jti is
// single-use, so a re-exchange must present whatever the file holds now, not
// the token read at Configure time. A one-second access token puts the second
// request inside the SDK's mandatory refresh window.
func TestFederationRereadsTheTokenFileOnEveryExchange(t *testing.T) {
	api := newFakeAPI(t, 1)
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", api.URL)
	tokenFile := writeIdentityToken(t, "jwt-A")

	pd := providerDataFrom(t, configureProviderWith(t, api.Client(), federationAttrs(tokenFile)))

	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	if err := os.WriteFile(tokenFile, []byte("jwt-B"), 0o600); err != nil {
		t.Fatalf("rotate identity token: %v", err)
	}
	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("second request failed: %v", err)
	}

	exchanges, apiCalls := api.snapshot()
	if len(exchanges) != 2 {
		t.Fatalf("token exchanges = %d, want 2 (re-exchange before expiry)", len(exchanges))
	}
	if got := exchanges[1].body["assertion"]; got != "jwt-B" {
		t.Errorf("second exchange assertion = %v, want the rotated token", got)
	}
	if got := apiCalls[1].Get("Authorization"); got != "Bearer at-2" {
		t.Errorf("second API authorization = %q, want the re-minted bearer", got)
	}
}

func TestFederationInlineIdentityToken(t *testing.T) {
	api := newFakeAPI(t, 3600)
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", api.URL)

	pd := providerDataFrom(t, configureProviderWith(t, api.Client(), map[string]tftypes.Value{
		"identity_token":     str("jwt-inline"),
		"federation_rule_id": str(testRuleID),
		"organization_id":    str(testOrgID),
	}))

	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	exchanges, _ := api.snapshot()
	if len(exchanges) != 1 || exchanges[0].body["assertion"] != "jwt-inline" {
		t.Fatalf("exchanges = %+v, want one with the inline token", exchanges)
	}
	for _, optional := range []string{"service_account_id", "workspace_id"} {
		if _, present := exchanges[0].body[optional]; present {
			t.Errorf("exchange body carries %s although it was not configured", optional)
		}
	}
}

func TestFederationFromEnvironment(t *testing.T) {
	api := newFakeAPI(t, 3600)
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", api.URL)
	t.Setenv(envIdentityTokenFile, writeIdentityToken(t, "jwt-env"))
	t.Setenv(envFederationRuleID, testRuleID)
	t.Setenv(envOrganizationID, testOrgID)
	t.Setenv(envServiceAccountID, testServiceAcct)
	t.Setenv(envWorkspaceID, "default")

	pd := providerDataFrom(t, configureProviderWith(t, api.Client(), nil))

	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	exchanges, _ := api.snapshot()
	if len(exchanges) != 1 {
		t.Fatalf("token exchanges = %d, want 1", len(exchanges))
	}
	body := exchanges[0].body
	if body["assertion"] != "jwt-env" || body["federation_rule_id"] != testRuleID ||
		body["organization_id"] != testOrgID || body["service_account_id"] != testServiceAcct ||
		body["workspace_id"] != "default" {
		t.Errorf("exchange body = %v, want the environment's values", body)
	}
}

func TestFederationConfigOverridesEnvironment(t *testing.T) {
	api := newFakeAPI(t, 3600)
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", api.URL)
	t.Setenv(envIdentityTokenFile, writeIdentityToken(t, "jwt-env"))
	t.Setenv(envFederationRuleID, "fdrl_FROMENV")
	t.Setenv(envOrganizationID, testOrgID)

	pd := providerDataFrom(t, configureProviderWith(t, api.Client(), map[string]tftypes.Value{
		"identity_token_file": str(writeIdentityToken(t, "jwt-config")),
		"federation_rule_id":  str(testRuleID),
	}))

	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	exchanges, _ := api.snapshot()
	body := exchanges[0].body
	if body["assertion"] != "jwt-config" || body["federation_rule_id"] != testRuleID {
		t.Errorf("exchange body = %v, want the configured values over the environment's", body)
	}
	if body["organization_id"] != testOrgID {
		t.Errorf("organization_id = %v, want the environment fallback", body["organization_id"])
	}
}

func TestFederationRejectsPartialConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		attrs map[string]tftypes.Value
		want  string
	}{
		{
			name:  "rule without token",
			attrs: map[string]tftypes.Value{"federation_rule_id": str(testRuleID), "organization_id": str(testOrgID)},
			want:  "no identity token",
		},
		{
			name:  "token without rule",
			attrs: map[string]tftypes.Value{"identity_token": str("jwt"), "organization_id": str(testOrgID)},
			want:  "federation_rule_id",
		},
		{
			name:  "token and rule without organization",
			attrs: map[string]tftypes.Value{"identity_token": str("jwt"), "federation_rule_id": str(testRuleID)},
			want:  "organization_id",
		},
		{
			name:  "only a workspace id",
			attrs: map[string]tftypes.Value{"workspace_id": str(testWorkspace)},
			want:  "partially configured",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearCredentialEnv(t)
			resp := configureProvider(t, tc.attrs)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected an error for a partial federation configuration")
			}
			joined := diagnosticsText(resp)
			if !strings.Contains(joined, tc.want) {
				t.Errorf("diagnostics %q do not mention %q", joined, tc.want)
			}
			if strings.Contains(joined, "Missing Credentials") {
				t.Error("a partial federation configuration must not be reported as no credential at all")
			}
		})
	}
}

func TestFederationRejectsBothTokenSources(t *testing.T) {
	clearCredentialEnv(t)
	resp := configureProvider(t, map[string]tftypes.Value{
		"identity_token":      str("jwt"),
		"identity_token_file": str(writeIdentityToken(t, "jwt")),
		"federation_rule_id":  str(testRuleID),
		"organization_id":     str(testOrgID),
	})
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error when both identity_token and identity_token_file are set")
	}
	if got := diagnosticsText(resp); !strings.Contains(got, "exactly one") {
		t.Errorf("diagnostics %q do not say to set exactly one token source", got)
	}
}

func TestFederationRejectsMalformedIDs(t *testing.T) {
	tests := []struct {
		name, attr, value, want string
	}{
		{"rule id prefix", "federation_rule_id", "rule_123", `"fdrl_"`},
		{"organization uuid", "organization_id", "my-org", "UUID"},
		{"service account prefix", "service_account_id", "sa_123", `"svac_"`},
		{"workspace prefix", "workspace_id", "ws_123", `"wrkspc_"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearCredentialEnv(t)
			attrs := federationAttrs(writeIdentityToken(t, "jwt"))
			attrs[tc.attr] = str(tc.value)
			resp := configureProvider(t, attrs)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected %s=%q to be rejected", tc.attr, tc.value)
			}
			errs := resp.Diagnostics.Errors()
			if len(errs) != 1 {
				t.Fatalf("errors = %d, want exactly 1: %v", len(errs), errs)
			}
			if !strings.Contains(errs[0].Detail(), tc.want) {
				t.Errorf("detail %q does not mention %s", errs[0].Detail(), tc.want)
			}
		})
	}
}

func TestFederationRejectsMissingTokenFile(t *testing.T) {
	clearCredentialEnv(t)
	resp := configureProvider(t, federationAttrs(filepath.Join(t.TempDir(), "absent")))
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error when the identity token file does not exist")
	}
	if got := diagnosticsText(resp); !strings.Contains(got, "not readable") {
		t.Errorf("diagnostics %q do not report the unreadable file", got)
	}
}

// TestAuthTokenTakesPrecedenceOverFederation: a static bearer wins, the
// federation settings are ignored with a warning, and no exchange happens.
func TestAuthTokenTakesPrecedenceOverFederation(t *testing.T) {
	api := newFakeAPI(t, 3600)
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", api.URL)

	attrs := federationAttrs(writeIdentityToken(t, "jwt-A"))
	attrs["auth_token"] = str("sk-ant-oat01-static")
	resp := configureProviderWith(t, api.Client(), attrs)
	pd := providerDataFrom(t, resp)

	if resp.Diagnostics.WarningsCount() != 1 {
		t.Errorf("warnings = %d, want 1 announcing the ignored federation settings: %v", resp.Diagnostics.WarningsCount(), resp.Diagnostics)
	}

	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	exchanges, apiCalls := api.snapshot()
	if len(exchanges) != 0 {
		t.Errorf("token exchanges = %d, want 0 when a static bearer is configured", len(exchanges))
	}
	if got := apiCalls[0].Get("Authorization"); got != "Bearer sk-ant-oat01-static" {
		t.Errorf("authorization = %q, want the static bearer", got)
	}
}

// diagnosticsText flattens every diagnostic for substring assertions.
func diagnosticsText(resp *provider.ConfigureResponse) string {
	var b strings.Builder
	for _, d := range resp.Diagnostics {
		b.WriteString(d.Summary())
		b.WriteString(": ")
		b.WriteString(d.Detail())
		b.WriteString("\n")
	}
	return b.String()
}
