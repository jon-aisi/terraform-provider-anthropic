// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// clearCredentialEnv unsets every variable the provider reads, so a test only
// sees the environment it sets itself. A leaked credential would change which
// headers a client sends, and a leaked base URL would send the request
// somewhere the test does not control.
//
// The variables are genuinely unset, not set to "": ANTHROPIC_BASE_URL is only
// skipped when it is empty, so an exported-but-empty value is a third state
// that neither the provider nor a future test should have to reason about.
// t.Setenv runs first purely for the cleanup it registers, which restores the
// original value (or its absence) at the end of the test.
func clearCredentialEnv(t *testing.T) {
	t.Helper()

	for _, k := range []string{
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_ADMIN_API_KEY",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_BASE_URL",
		"ANTHROPIC_CONFIG_DIR",
		"ANTHROPIC_PROFILE",
		envIdentityToken,
		envIdentityTokenFile,
		envFederationRuleID,
		envOrganizationID,
		envServiceAccountID,
		envWorkspaceID,
	} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
	}
}

// configureProvider runs Configure against a config holding attrs, with every
// other provider attribute null, using the default HTTP client.
func configureProvider(t *testing.T, attrs map[string]tftypes.Value) *provider.ConfigureResponse {
	t.Helper()
	return configureProviderWith(t, nil, attrs)
}

// configureProviderWith is configureProvider with the HTTP client every
// built client should use; tests pass an httptest TLS server's client so the
// self-signed certificate is trusted.
func configureProviderWith(t *testing.T, hc *http.Client, attrs map[string]tftypes.Value) *provider.ConfigureResponse {
	t.Helper()

	ctx := context.Background()
	p := &AnthropicProvider{version: "test", httpClient: hc}

	schemaResp := &provider.SchemaResponse{}
	p.Schema(ctx, provider.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("provider schema: %v", schemaResp.Diagnostics)
	}

	objType, ok := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatal("provider schema is not an object type")
	}

	values := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, attrType := range objType.AttributeTypes {
		if v, found := attrs[name]; found {
			values[name] = v
			continue
		}
		values[name] = tftypes.NewValue(attrType, nil)
	}

	resp := &provider.ConfigureResponse{}
	p.Configure(ctx, provider.ConfigureRequest{
		Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objType, values)},
	}, resp)

	return resp
}

func providerDataFrom(t *testing.T, resp *provider.ConfigureResponse) *providerdata.ProviderData {
	t.Helper()

	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure returned errors: %v", resp.Diagnostics)
	}
	pd, ok := resp.ResourceData.(*providerdata.ProviderData)
	if !ok {
		t.Fatalf("ResourceData is %T, want *providerdata.ProviderData", resp.ResourceData)
	}
	if resp.DataSourceData != resp.ResourceData {
		t.Error("DataSourceData and ResourceData must be the same ProviderData")
	}
	return pd
}

func str(v string) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }

// recordingServer is a TLS httptest server that answers every request with
// `{}` and keeps the headers of the last one.
type recordingServer struct {
	*httptest.Server
	mu   sync.Mutex
	last http.Header
}

func newRecordingServer(t *testing.T) *recordingServer {
	t.Helper()
	rs := &recordingServer{}
	rs.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		rs.last = r.Header.Clone()
		rs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (rs *recordingServer) lastHeader() http.Header {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.last
}

func TestConfigureRequiresAtLeastOneCredential(t *testing.T) {
	clearCredentialEnv(t)

	resp := configureProvider(t, nil)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error when no credential is configured")
	}
	if got := resp.Diagnostics.Errors()[0].Summary(); got != "Missing Credentials" {
		t.Errorf("summary = %q, want %q", got, "Missing Credentials")
	}
	detail := resp.Diagnostics.Errors()[0].Detail()
	for _, want := range []string{"auth_token", "identity_token_file", "admin_api_key"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q does not offer %s", detail, want)
		}
	}
}

// TestConfigureAuthTokenAloneIsSufficient covers the WIF-only setup: an
// operator managing federation resources has no API key at all.
func TestConfigureAuthTokenAloneIsSufficient(t *testing.T) {
	clearCredentialEnv(t)

	resp := configureProvider(t, map[string]tftypes.Value{
		"auth_token": str("sk-ant-oat01-config"),
	})
	pd := providerDataFrom(t, resp)

	if pd.OAuthClient == nil {
		t.Error("OAuthClient is nil, want a client built from auth_token")
	}
	if pd.AdminClient != nil {
		t.Error("AdminClient should be nil when admin_api_key is not configured")
	}
}

func TestConfigureAuthTokenFromEnvironment(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "sk-ant-oat01-env")

	pd := providerDataFrom(t, configureProvider(t, nil))

	if pd.OAuthClient == nil {
		t.Error("OAuthClient is nil, want a client built from ANTHROPIC_AUTH_TOKEN")
	}
}

func TestConfigureAdminKeyAloneBuildsOnlyTheAdminClient(t *testing.T) {
	clearCredentialEnv(t)

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"admin_api_key": str("sk-ant-admin03-x"),
	}))

	if pd.AdminClient == nil {
		t.Error("AdminClient is nil")
	}
	if pd.OAuthClient != nil {
		t.Error("OAuthClient should be nil: an Admin API key must never stand in for the bearer the WIF endpoints need")
	}
}

func TestConfigureBuildsEveryConfiguredClient(t *testing.T) {
	clearCredentialEnv(t)

	resp := configureProvider(t, map[string]tftypes.Value{
		"admin_api_key": str("sk-ant-admin03-x"),
		"auth_token":    str("sk-ant-oat01-x"),
	})
	pd := providerDataFrom(t, resp)

	if pd.AdminClient == nil {
		t.Error("AdminClient is nil")
	}
	if pd.OAuthClient == nil {
		t.Error("OAuthClient is nil")
	}
}

// TestConfigureOAuthClientCarriesOnlyTheBearer is the regression test for the
// SDK's default credential chain: anthropic.NewClient prepends options derived
// from ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN before our explicit option
// runs, so without option.WithoutEnvironmentDefaults the client would present
// both credentials. The WIF endpoints reject an API key outright, so the stray
// header is not cosmetic.
func TestConfigureOAuthClientCarriesOnlyTheBearer(t *testing.T) {
	srv := newRecordingServer(t)

	clearCredentialEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-env")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "sk-ant-oat01-env")

	pd := providerDataFrom(t, configureProviderWith(t, srv.Client(), nil))

	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	got := srv.lastHeader()
	if got.Get("Authorization") != "Bearer sk-ant-oat01-env" {
		t.Errorf("authorization = %q, want the bearer token", got.Get("Authorization"))
	}
	if v := got.Get("X-Api-Key"); v != "" {
		t.Errorf("oauth client also sent x-api-key: %q", v)
	}
}

// TestConfigureIgnoresTheAmbientProfile covers the credential sources beyond
// the env vars. `ant auth login --profile admin` — the very command the
// provider docs tell operators to run — writes a profile file and makes it the
// active one, and the SDK's chain reaches it whenever no credential variable
// is exported. option.WithConfig then applies that profile's non-credential
// settings unconditionally, so its workspace_id would be stamped on every
// request (and its base_url would win over the production default) even though
// the Terraform configuration named a credential explicitly and never
// mentioned a workspace.
func TestConfigureIgnoresTheAmbientProfile(t *testing.T) {
	srv := newRecordingServer(t)

	clearCredentialEnv(t)
	writeProfile(t, "admin", `{
		"version": "1.0",
		"authentication": {"type": "user_oauth"},
		"base_url": "https://profile.example.invalid",
		"workspace_id": "wrkspc_from_the_ambient_profile"
	}`)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)

	pd := providerDataFrom(t, configureProviderWith(t, srv.Client(), map[string]tftypes.Value{
		"auth_token": str("sk-ant-oat01-config"),
	}))

	if err := pd.OAuthClient.Get(context.Background(), "/v1/models", nil, nil); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	got := srv.lastHeader()
	if v := got.Get("anthropic-workspace-id"); v != "" {
		t.Errorf("client inherited the profile's workspace scoping: anthropic-workspace-id = %q", v)
	}
	if got.Get("Authorization") != "Bearer sk-ant-oat01-config" {
		t.Errorf("authorization = %q, want the configured bearer token", got.Get("Authorization"))
	}
}

// writeProfile installs a profile file the SDK's credential chain would pick
// up, in a config directory scoped to this test. ANTHROPIC_PROFILE selects it
// explicitly, which is the step-3 source; the fallback active-profile lookup
// (step 5) reads the same file through the same option.WithConfig.
func writeProfile(t *testing.T, name, contents string) {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "configs"), 0o700); err != nil {
		t.Fatalf("create profile dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "configs", name+".json"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	t.Setenv("ANTHROPIC_CONFIG_DIR", dir)
	t.Setenv("ANTHROPIC_PROFILE", name)
}

// TestSchemaMarksCredentialsSensitive pins which attributes Terraform must
// redact: every value that is itself a secret. Paths and IDs are not.
func TestSchemaMarksCredentialsSensitive(t *testing.T) {
	resp := &provider.SchemaResponse{}
	(&AnthropicProvider{}).Schema(context.Background(), provider.SchemaRequest{}, resp)

	want := map[string]bool{
		"base_url":            false,
		"admin_api_key":       true,
		"auth_token":          true,
		"identity_token":      true,
		"identity_token_file": false,
		"federation_rule_id":  false,
		"organization_id":     false,
		"service_account_id":  false,
		"workspace_id":        false,
	}
	for name, wantSensitive := range want {
		attr, ok := resp.Schema.Attributes[name]
		if !ok {
			t.Errorf("attribute %s missing from schema", name)
			continue
		}
		if attr.IsSensitive() != wantSensitive {
			t.Errorf("%s: Sensitive = %v, want %v", name, attr.IsSensitive(), wantSensitive)
		}
	}
	for name := range resp.Schema.Attributes {
		if _, known := want[name]; !known {
			t.Errorf("attribute %s is not covered by this test; decide whether it is Sensitive", name)
		}
	}
}

func TestResolveCredential(t *testing.T) {
	const envVar = "ANTHROPIC_TEST_CREDENTIAL"

	tests := []struct {
		name        string
		configValue types.String
		env         string
		want        string
	}{
		{
			name:        "config wins over environment",
			configValue: types.StringValue("from-config"),
			env:         "from-env",
			want:        "from-config",
		},
		{
			name:        "null config falls back to environment",
			configValue: types.StringNull(),
			env:         "from-env",
			want:        "from-env",
		},
		{
			name:        "unknown config falls back to environment",
			configValue: types.StringUnknown(),
			env:         "from-env",
			want:        "from-env",
		},
		{
			name:        "nothing configured yields an empty credential",
			configValue: types.StringNull(),
			env:         "",
			want:        "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envVar, tc.env)
			if got := resolveCredential(tc.configValue, envVar); got != tc.want {
				t.Errorf("resolveCredential() = %q, want %q", got, tc.want)
			}
		})
	}
}
