// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Environment variables read when the matching provider attribute is unset.
// The names are the ones the Anthropic SDKs use for their own federation
// auto-discovery, so a workload configured for the SDK configures the
// provider too.
const (
	envIdentityToken     = "ANTHROPIC_IDENTITY_TOKEN"
	envIdentityTokenFile = "ANTHROPIC_IDENTITY_TOKEN_FILE"
	envFederationRuleID  = "ANTHROPIC_FEDERATION_RULE_ID"
	envOrganizationID    = "ANTHROPIC_ORGANIZATION_ID"
	envServiceAccountID  = "ANTHROPIC_SERVICE_ACCOUNT_ID"
	envWorkspaceID       = "ANTHROPIC_WORKSPACE_ID"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// federationSetting is one resolved federation attribute together with where
// its value came from, so a diagnostic can point at the attribute when the
// operator set it in configuration and at the variable otherwise.
type federationSetting struct {
	attr  string
	env   string
	value string
	// fromConfig is true when value came from the provider block rather than
	// the environment.
	fromConfig bool
}

func (s federationSetting) set() bool { return s.value != "" }

// source names where the value came from, for diagnostics.
func (s federationSetting) source() string {
	if s.fromConfig {
		return "the " + s.attr + " provider argument"
	}
	return "the " + s.env + " environment variable"
}

// federationConfig is the workload identity federation configuration
// resolved from the provider block and the environment.
type federationConfig struct {
	identityToken     federationSetting
	identityTokenFile federationSetting
	federationRuleID  federationSetting
	organizationID    federationSetting
	serviceAccountID  federationSetting
	workspaceID       federationSetting
}

func resolveFederationSetting(attr, env string, configValue types.String) federationSetting {
	if !configValue.IsNull() && !configValue.IsUnknown() {
		return federationSetting{attr: attr, env: env, value: configValue.ValueString(), fromConfig: true}
	}
	return federationSetting{attr: attr, env: env, value: os.Getenv(env)}
}

func resolveFederation(data AnthropicProviderModel) federationConfig {
	return federationConfig{
		identityToken:     resolveFederationSetting("identity_token", envIdentityToken, data.IdentityToken),
		identityTokenFile: resolveFederationSetting("identity_token_file", envIdentityTokenFile, data.IdentityTokenFile),
		federationRuleID:  resolveFederationSetting("federation_rule_id", envFederationRuleID, data.FederationRuleID),
		organizationID:    resolveFederationSetting("organization_id", envOrganizationID, data.OrganizationID),
		serviceAccountID:  resolveFederationSetting("service_account_id", envServiceAccountID, data.ServiceAccountID),
		workspaceID:       resolveFederationSetting("workspace_id", envWorkspaceID, data.WorkspaceID),
	}
}

// configured reports whether any federation setting is present. A partial
// configuration is still "configured": validate turns it into an error
// rather than letting the provider fall through to Missing Credentials.
func (c federationConfig) configured() bool {
	for _, s := range c.settings() {
		if s.set() {
			return true
		}
	}
	return false
}

func (c federationConfig) settings() []federationSetting {
	return []federationSetting{
		c.identityToken, c.identityTokenFile, c.federationRuleID,
		c.organizationID, c.serviceAccountID, c.workspaceID,
	}
}

// validate checks the resolved configuration is complete and well-formed.
// The ID formats are the tagged-ID prefixes the token endpoint enforces;
// rejecting them here turns a 400 at first API call into a Configure error.
func (c federationConfig) validate(diags *diag.Diagnostics) {
	const summary = "Invalid Workload Identity Federation Configuration"

	addError := func(s federationSetting, detail string) {
		if s.fromConfig {
			diags.AddAttributeError(path.Root(s.attr), summary, detail)
			return
		}
		diags.AddError(summary, detail)
	}

	switch {
	case c.identityToken.set() && c.identityTokenFile.set():
		addError(c.identityTokenFile, fmt.Sprintf(
			"Both an inline identity token (%s) and an identity token file (%s) are configured; set exactly one.",
			c.identityToken.source(), c.identityTokenFile.source()))
	case !c.identityToken.set() && !c.identityTokenFile.set():
		diags.AddError(summary,
			"Workload identity federation is partially configured: no identity token. "+
				"Set identity_token_file ("+envIdentityTokenFile+") to the path of the workload's OIDC token, "+
				"or identity_token ("+envIdentityToken+") to the token itself.")
	}

	if !c.federationRuleID.set() {
		diags.AddError(summary,
			"Workload identity federation is partially configured: federation_rule_id ("+envFederationRuleID+") is required.")
	} else if !strings.HasPrefix(c.federationRuleID.value, "fdrl_") {
		addError(c.federationRuleID, fmt.Sprintf(
			"federation_rule_id from %s must be a federation rule ID starting with \"fdrl_\".", c.federationRuleID.source()))
	}

	if !c.organizationID.set() {
		diags.AddError(summary,
			"Workload identity federation is partially configured: organization_id ("+envOrganizationID+") is required.")
	} else if !uuidPattern.MatchString(c.organizationID.value) {
		addError(c.organizationID, fmt.Sprintf(
			"organization_id from %s must be the organization UUID.", c.organizationID.source()))
	}

	if c.serviceAccountID.set() && !strings.HasPrefix(c.serviceAccountID.value, "svac_") {
		addError(c.serviceAccountID, fmt.Sprintf(
			"service_account_id from %s must be a service account ID starting with \"svac_\".", c.serviceAccountID.source()))
	}

	if c.workspaceID.set() && c.workspaceID.value != "default" && !strings.HasPrefix(c.workspaceID.value, "wrkspc_") {
		addError(c.workspaceID, fmt.Sprintf(
			"workspace_id from %s must be a workspace ID starting with \"wrkspc_\", or \"default\".", c.workspaceID.source()))
	}

	if c.identityTokenFile.set() {
		if _, err := os.Stat(c.identityTokenFile.value); err != nil {
			addError(c.identityTokenFile, fmt.Sprintf(
				"The identity token file from %s is not readable: %s", c.identityTokenFile.source(), err))
		}
	}
}

// requestOption returns the SDK option that authenticates requests through
// the RFC 7523 jwt-bearer exchange at <base URL>/v1/oauth/token. The SDK
// caches the access token and re-exchanges before it expires, calling the
// identity token function afresh each time; with identity_token_file that
// re-reads the file, so a rotated token is picked up. An inline
// identity_token is returned unchanged on every exchange, which fails once
// the issuer rejects the reused jti.
func (c federationConfig) requestOption() option.RequestOption {
	var provider option.IdentityTokenFunc
	if c.identityTokenFile.set() {
		provider = option.IdentityTokenFile(c.identityTokenFile.value)
	} else {
		token := c.identityToken.value
		provider = func(context.Context) (string, error) { return token, nil }
	}

	return option.WithFederationTokenProvider(provider, option.FederationOptions{
		FederationRuleID: c.federationRuleID.value,
		OrganizationID:   c.organizationID.value,
		ServiceAccountID: c.serviceAccountID.value,
		WorkspaceID:      c.workspaceID.value,
	})
}
