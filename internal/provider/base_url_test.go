// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestBaseURLDefaultsToProduction(t *testing.T) {
	tests := []struct {
		name  string
		attrs map[string]tftypes.Value
	}{
		{"unset", map[string]tftypes.Value{"admin_api_key": str("sk-ant-admin03-x")}},
		{"the default spelled out", map[string]tftypes.Value{"admin_api_key": str("sk-ant-admin03-x"), "base_url": str(defaultBaseURL + "/")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearCredentialEnv(t)

			resp := configureProvider(t, tc.attrs)
			pd := providerDataFrom(t, resp)

			if pd.AdminClient.BaseURL != defaultBaseURL {
				t.Errorf("admin client base URL = %q, want %q", pd.AdminClient.BaseURL, defaultBaseURL)
			}
			if resp.Diagnostics.WarningsCount() != 0 {
				t.Errorf("the production origin must not be warned about: %v", resp.Diagnostics)
			}
		})
	}
}

// TestBaseURLWarnsWhenNotTheDefault: the destination of every credential can
// be set from the environment alone, so anything but the production origin
// is announced with its host and where the value came from.
func TestBaseURLWarnsWhenNotTheDefault(t *testing.T) {
	tests := []struct {
		name, config, env, wantHost, wantSource string
	}{
		{"from config", "https://mock.example.test:8443/", "", "mock.example.test:8443", "base_url provider argument"},
		{"from environment", "", "https://env.example.test", "env.example.test", envBaseURL + " environment variable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearCredentialEnv(t)
			if tc.env != "" {
				t.Setenv(envBaseURL, tc.env)
			}
			attrs := map[string]tftypes.Value{"admin_api_key": str("sk-ant-admin03-x")}
			if tc.config != "" {
				attrs["base_url"] = str(tc.config)
			}

			resp := configureProvider(t, attrs)
			providerDataFrom(t, resp)

			warnings := resp.Diagnostics.Warnings()
			if len(warnings) != 1 {
				t.Fatalf("warnings = %d, want 1: %v", len(warnings), resp.Diagnostics)
			}
			if got := warnings[0].Summary(); got != "Non-default API Destination" {
				t.Errorf("summary = %q, want Non-default API Destination", got)
			}
			for _, want := range []string{tc.wantHost, tc.wantSource, defaultBaseURL} {
				if !strings.Contains(warnings[0].Detail(), want) {
					t.Errorf("detail %q does not mention %q", warnings[0].Detail(), want)
				}
			}
		})
	}
}

func TestBaseURLFromEnvironment(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv(envBaseURL, "https://mock.example.test/")

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"admin_api_key": str("sk-ant-admin03-x"),
	}))

	if pd.AdminClient.BaseURL != "https://mock.example.test" {
		t.Errorf("admin client base URL = %q, want the environment value without its trailing slash", pd.AdminClient.BaseURL)
	}
}

func TestBaseURLConfigOverridesEnvironment(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv(envBaseURL, "https://from-env.example.test")

	pd := providerDataFrom(t, configureProvider(t, map[string]tftypes.Value{
		"admin_api_key": str("sk-ant-admin03-x"),
		"base_url":      str("https://from-config.example.test"),
	}))

	if pd.AdminClient.BaseURL != "https://from-config.example.test" {
		t.Errorf("admin client base URL = %q, want the configured value", pd.AdminClient.BaseURL)
	}
}

// TestBaseURLRejectsInsecureAndMalformedValues: the base URL receives a
// credential header on every request, so anything but a clean https origin is
// refused at Configure time, before any request is made.
func TestBaseURLRejectsInsecureAndMalformedValues(t *testing.T) {
	tests := []struct {
		name, value, want string
	}{
		{"plain http", "http://api.anthropic.com", "https"},
		{"http on loopback", "http://127.0.0.1:8080", "https"},
		{"no scheme", "api.anthropic.com", "https"},
		{"query string", "https://api.anthropic.com/?x=1", "query"},
		{"fragment", "https://api.anthropic.com/#frag", "fragment"},
		{"user info", "https://user:pass@api.anthropic.com", "user info"},
		{"unparseable", "https://exa mple.com", "not a valid URL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearCredentialEnv(t)
			resp := configureProvider(t, map[string]tftypes.Value{
				"admin_api_key": str("sk-ant-admin03-x"),
				"base_url":      str(tc.value),
			})
			if !resp.Diagnostics.HasError() {
				t.Fatalf("base_url=%q was accepted", tc.value)
			}
			errs := resp.Diagnostics.Errors()
			if errs[0].Summary() != "Invalid Base URL" {
				t.Errorf("summary = %q, want Invalid Base URL", errs[0].Summary())
			}
			if !strings.Contains(errs[0].Detail(), tc.want) {
				t.Errorf("detail %q does not mention %q", errs[0].Detail(), tc.want)
			}
			if resp.ResourceData != nil {
				t.Error("no client must be built when the base URL is rejected")
			}
		})
	}
}

func TestBaseURLFromEnvironmentIsValidatedToo(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv(envBaseURL, "http://api.anthropic.com")

	resp := configureProvider(t, map[string]tftypes.Value{"admin_api_key": str("x")})
	if !resp.Diagnostics.HasError() {
		t.Fatal("an http:// ANTHROPIC_BASE_URL was accepted")
	}
	if detail := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, envBaseURL) {
		t.Errorf("detail %q does not name the environment variable the value came from", detail)
	}
}

// TestBaseURLIsUsedByBothClients: the hand-rolled admin client and the SDK
// client must agree on where requests go, and each must carry only its own
// credential there.
func TestBaseURLIsUsedByBothClients(t *testing.T) {
	var (
		mu   sync.Mutex
		seen = map[string]http.Header{}
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	clearCredentialEnv(t)
	pd := providerDataFrom(t, configureProviderWith(t, srv, map[string]tftypes.Value{
		"base_url":      str(srv.URL + "/"),
		"admin_api_key": str("sk-ant-admin03-x"),
		"auth_token":    str("sk-ant-oat01-x"),
	}))

	if _, err := pd.AdminClient.DoRequest(context.Background(), http.MethodGet, "/v1/organizations/workspaces/wrkspc_1", nil); err != nil {
		t.Fatalf("admin request failed: %v", err)
	}
	if err := pd.OAuthClient.Get(context.Background(), "/v1/organizations/service_accounts", nil, nil); err != nil {
		t.Fatalf("oauth request failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	adminHdr, ok := seen["/v1/organizations/workspaces/wrkspc_1"]
	if !ok {
		t.Fatalf("admin request did not reach the configured base URL; paths seen: %v", pathsOf(seen))
	}
	if adminHdr.Get("X-Api-Key") != "sk-ant-admin03-x" || adminHdr.Get("Authorization") != "" {
		t.Errorf("admin request headers: x-api-key=%q authorization=%q", adminHdr.Get("X-Api-Key"), adminHdr.Get("Authorization"))
	}
	oauthHdr, ok := seen["/v1/organizations/service_accounts"]
	if !ok {
		t.Fatalf("oauth request did not reach the configured base URL; paths seen: %v", pathsOf(seen))
	}
	if oauthHdr.Get("Authorization") != "Bearer sk-ant-oat01-x" || oauthHdr.Get("X-Api-Key") != "" {
		t.Errorf("oauth request headers: authorization=%q x-api-key=%q", oauthHdr.Get("Authorization"), oauthHdr.Get("X-Api-Key"))
	}
}

func pathsOf(m map[string]http.Header) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestBaseURLPathWarnsWithFederation: the SDK drops the path from the token
// exchange URL, so a base URL with a path splits the destinations.
func TestBaseURLPathWarnsWithFederation(t *testing.T) {
	const summary = "Base URL Path Ignored By The Token Exchange"
	tests := []struct {
		name    string
		baseURL string
		static  bool
		want    bool
	}{
		{"federation with a path", "https://proxy.example.test/anthropic/", false, true},
		{"federation without a path", "https://proxy.example.test", false, false},
		{"static token with a path", "https://proxy.example.test/anthropic", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearCredentialEnv(t)
			attrs := map[string]tftypes.Value{"base_url": str(tc.baseURL)}
			if tc.static {
				attrs["auth_token"] = str("sk-ant-oat01-x")
			} else {
				for k, v := range federationAttrs(writeIdentityToken(t, "jwt")) {
					attrs[k] = v
				}
			}

			resp := configureProvider(t, attrs)
			providerDataFrom(t, resp)

			got := slices.Contains(warningSummaries(resp), summary)
			if got != tc.want {
				t.Fatalf("warned = %v, want %v: %v", got, tc.want, resp.Diagnostics)
			}
			if !tc.want {
				return
			}
			var detail string
			for _, w := range resp.Diagnostics.Warnings() {
				if w.Summary() == summary {
					detail = w.Detail()
				}
			}
			for _, want := range []string{"https://proxy.example.test/anthropic", "https://proxy.example.test/v1/oauth/token"} {
				if !strings.Contains(detail, want) {
					t.Errorf("detail %q does not name %q", detail, want)
				}
			}
		})
	}
}
