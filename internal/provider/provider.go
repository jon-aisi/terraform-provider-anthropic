// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/federation"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/serviceaccounts"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/workspaces"
)

// Ensure AnthropicProvider satisfies various provider interfaces.
var _ provider.Provider = &AnthropicProvider{}

// AnthropicProvider defines the provider implementation.
type AnthropicProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string

	// httpClient, when set, is used by every client the provider builds.
	// Tests set it to an httptest TLS server's client; production leaves it
	// nil and each client uses its own default.
	httpClient *http.Client
}

// AnthropicProviderModel describes the provider data model.
type AnthropicProviderModel struct {
	BaseURL           types.String `tfsdk:"base_url"`
	AdminApiKey       types.String `tfsdk:"admin_api_key"`
	AuthToken         types.String `tfsdk:"auth_token"`
	IdentityToken     types.String `tfsdk:"identity_token"`
	IdentityTokenFile types.String `tfsdk:"identity_token_file"`
	FederationRuleID  types.String `tfsdk:"federation_rule_id"`
	OrganizationID    types.String `tfsdk:"organization_id"`
	ServiceAccountID  types.String `tfsdk:"service_account_id"`
	WorkspaceID       types.String `tfsdk:"workspace_id"`
}

func (p *AnthropicProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "anthropic"
	resp.Version = p.version
}

func (p *AnthropicProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the Workload Identity Federation subset of the Anthropic Admin API. " +
			"The federation resources and data sources authenticate with an `org:admin` OAuth bearer token, " +
			"supplied either statically (`auth_token`) or minted from the workload's own OIDC identity token " +
			"(`identity_token_file` with `federation_rule_id` and `organization_id`). " +
			"An Admin API key (`admin_api_key`) is **not accepted** by the federation endpoints; it is only used by `anthropic_workspace`.",
		Attributes: map[string]schema.Attribute{
			"base_url": schema.StringAttribute{
				Optional: true,
				Description: "Origin of the Anthropic API, used by every request including the federation token exchange. " +
					"Defaults to `https://api.anthropic.com`; https only. Override it only to point tests at a local server. " +
					"Can also be set via the ANTHROPIC_BASE_URL environment variable.",
			},
			"admin_api_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "The Anthropic Admin API key. Used only by the `anthropic_workspace` resource and data sources; " +
					"the Workload Identity Federation endpoints reject it. " +
					"Can also be set via the ANTHROPIC_ADMIN_API_KEY environment variable.",
			},
			"auth_token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "An org:admin OAuth bearer token (`sk-ant-oat01-...`) for the Workload Identity Federation " +
					"admin endpoints. Takes precedence over workload identity federation when both are configured. " +
					"Can also be set via the ANTHROPIC_AUTH_TOKEN environment variable.",
			},
			"identity_token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "An OIDC identity token (JWT) exchanged for an org:admin access token through workload " +
					"identity federation. Prefer `identity_token_file`: the token is re-exchanged when the access token " +
					"expires, and a JWT carrying a single-use `jti` is accepted only once. " +
					"Can also be set via the ANTHROPIC_IDENTITY_TOKEN environment variable.",
			},
			"identity_token_file": schema.StringAttribute{
				Optional: true,
				Description: "Path to a file holding the OIDC identity token. Re-read before every exchange, so a " +
					"rotated token is picked up. Mutually exclusive with `identity_token`. " +
					"Can also be set via the ANTHROPIC_IDENTITY_TOKEN_FILE environment variable.",
			},
			"federation_rule_id": schema.StringAttribute{
				Optional: true,
				Description: "The federation rule (`fdrl_...`) that governs the exchange. Required with an identity token. " +
					"Can also be set via the ANTHROPIC_FEDERATION_RULE_ID environment variable.",
			},
			"organization_id": schema.StringAttribute{
				Optional: true,
				Description: "The organization UUID the federation rule belongs to. Required with an identity token. " +
					"Can also be set via the ANTHROPIC_ORGANIZATION_ID environment variable.",
			},
			"service_account_id": schema.StringAttribute{
				Optional: true,
				Description: "The service account (`svac_...`) the rule targets; an expected-target check for rules with " +
					"`target_type = SERVICE_ACCOUNT`. Can also be set via the ANTHROPIC_SERVICE_ACCOUNT_ID environment variable.",
			},
			"workspace_id": schema.StringAttribute{
				Optional: true,
				Description: "The workspace (`wrkspc_...`, or `default`) the minted token is scoped to. Required when the " +
					"rule is enabled for more than one workspace. Can also be set via the ANTHROPIC_WORKSPACE_ID environment variable.",
			},
		},
	}
}

func (p *AnthropicProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data AnthropicProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	baseURL := resolveBaseURL(data.BaseURL, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	adminApiKey := resolveCredential(data.AdminApiKey, "ANTHROPIC_ADMIN_API_KEY")
	authToken := resolveCredential(data.AuthToken, "ANTHROPIC_AUTH_TOKEN")
	fed := resolveFederation(data)

	if adminApiKey == "" && authToken == "" && !fed.configured() {
		resp.Diagnostics.AddError(
			"Missing Credentials",
			"At least one credential must be configured: auth_token (ANTHROPIC_AUTH_TOKEN) or workload identity "+
				"federation (identity_token_file / ANTHROPIC_IDENTITY_TOKEN_FILE with federation_rule_id and "+
				"organization_id) for the Workload Identity Federation resources, "+
				"or admin_api_key (ANTHROPIC_ADMIN_API_KEY) for anthropic_workspace.",
		)
		return
	}

	// Each client carries exactly the credential resolved above and nothing
	// the environment contributed on its own — see newSDKClient.
	pd := &providerdata.ProviderData{}
	if adminApiKey != "" {
		pd.AdminClient = admin.NewClient(adminApiKey)
		pd.AdminClient.BaseURL = baseURL
		if p.httpClient != nil {
			pd.AdminClient.HTTPClient = p.httpClient
		}
	}

	switch {
	case authToken != "":
		if fed.configured() {
			resp.Diagnostics.AddWarning(
				"Workload Identity Federation Settings Ignored",
				"auth_token (or ANTHROPIC_AUTH_TOKEN) is set and takes precedence; the identity token and federation "+
					"IDs are not used. Unset one of them to make the choice explicit.",
			)
		}
		pd.OAuthClient = &providerdata.OAuthClient{Client: p.newSDKClient(baseURL, option.WithAuthToken(authToken))}
	case fed.configured():
		fed.validate(&resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		pd.OAuthClient = &providerdata.OAuthClient{Client: p.newSDKClient(baseURL, fed.requestOption())}
	}

	resp.DataSourceData = pd
	resp.ResourceData = pd
}

// resolveCredential returns the credential to use for one authentication
// method: the provider argument when it is set, otherwise the environment
// variable. An unknown value (an unresolved reference at plan time) is treated
// as unset, so the environment still applies.
func resolveCredential(configValue types.String, envVar string) string {
	if !configValue.IsNull() && !configValue.IsUnknown() {
		return configValue.ValueString()
	}
	return os.Getenv(envVar)
}

// newSDKClient builds an SDK client that carries exactly the credential passed
// in, and nothing the environment contributed on its own.
//
// option.WithoutEnvironmentDefaults suppresses anthropic.DefaultClientOptions
// entirely, which matters for more than the credential headers. That chain has
// five sources: ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, the profile named by
// ANTHROPIC_PROFILE, env-var federation, and the fallback profile under
// ~/.anthropic. The first two set a header before our explicit option is
// applied, so with an API key exported alongside the bearer a client would
// send x-api-key *and* Authorization, and the federation endpoints reject the
// former. The three profile/federation sources go further: option.WithConfig
// applies the profile's non-credential settings unconditionally, so a profile
// left behind by `ant auth login` (which also makes itself the active
// profile) would silently override the base URL and stamp its workspace_id on
// every request — neither of which appears anywhere in the Terraform
// configuration. Federation is therefore resolved by the provider itself
// (see federation.go) rather than left to the SDK's env chain.
//
// The marker option also skips ANTHROPIC_BASE_URL, so the base URL resolved
// by resolveBaseURL is passed explicitly.
//
// The credential option goes last: the federation option captures the HTTP
// client in effect when it is applied and performs the token exchange with
// it, so option.WithHTTPClient has to precede it.
func (p *AnthropicProvider) newSDKClient(baseURL string, credential option.RequestOption) *anthropic.Client {
	opts := []option.RequestOption{option.WithoutEnvironmentDefaults(), option.WithBaseURL(baseURL)}
	if p.httpClient != nil {
		opts = append(opts, option.WithHTTPClient(p.httpClient))
	}
	opts = append(opts, credential)

	client := anthropic.NewClient(opts...)

	return &client
}

func (p *AnthropicProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		federation.NewFederationIssuerResource,
		federation.NewFederationRuleResource,
		federation.NewFederationRuleWorkspaceResource,
		serviceaccounts.NewServiceAccountResource,
		serviceaccounts.NewServiceAccountWorkspaceResource,
		workspaces.NewWorkspaceResource,
	}
}

func (p *AnthropicProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		federation.NewFederationIssuersDataSource,
		federation.NewFederationRulesDataSource,
		federation.NewFederationIssuerDataSource,
		federation.NewFederationRuleDataSource,
		federation.NewFederationRuleWorkspacesDataSource,
		serviceaccounts.NewServiceAccountsDataSource,
		serviceaccounts.NewServiceAccountDataSource,
		serviceaccounts.NewServiceAccountWorkspacesDataSource,
		workspaces.NewWorkspaceDataSource,
		workspaces.NewWorkspacesDataSource,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &AnthropicProvider{
			version: version,
		}
	}
}
