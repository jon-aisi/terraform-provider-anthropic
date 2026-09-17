// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	defaultBaseURL = "https://api.anthropic.com"
	envBaseURL     = "ANTHROPIC_BASE_URL"
)

// resolveBaseURL returns the origin every client sends requests to: the
// base_url argument, else ANTHROPIC_BASE_URL, else the production API. The
// value is validated rather than passed through because both the Admin API
// key and the minted bearer travel in a header on every request: a typo or an
// http:// scheme would ship them to the wrong place in clear text. Only https
// is accepted, with no query or fragment; a trailing slash is dropped so path
// concatenation in the admin client does not produce "//".
//
// Anything but the production origin is reported as a warning naming the host
// and where the value came from: the environment alone can set it, with
// nothing in the configuration to show every credential is going elsewhere.
func resolveBaseURL(configValue types.String, diags *diag.Diagnostics) string {
	const summary = "Invalid Base URL"

	raw := resolveCredential(configValue, envBaseURL)
	if raw == "" {
		return defaultBaseURL
	}

	fromConfig := !configValue.IsNull() && !configValue.IsUnknown()
	source := "the " + envBaseURL + " environment variable"
	if fromConfig {
		source = "the base_url provider argument"
	}

	addError := func(detail string) {
		if fromConfig {
			diags.AddAttributeError(path.Root("base_url"), summary, detail)
			return
		}
		diags.AddError(summary, envBaseURL+": "+detail)
	}

	u, err := url.Parse(raw)
	if err != nil {
		addError(fmt.Sprintf("%q is not a valid URL: %s", raw, err))
		return ""
	}
	switch {
	case u.Scheme != "https":
		addError(fmt.Sprintf("%q must use the https scheme; credentials are sent in a header on every request.", raw))
	case u.Host == "":
		addError(fmt.Sprintf("%q has no host.", raw))
	case u.RawQuery != "" || u.Fragment != "" || u.User != nil:
		addError(fmt.Sprintf("%q must not carry a query string, fragment or user info.", raw))
	}
	if diags.HasError() {
		return ""
	}

	resolved := strings.TrimRight(raw, "/")
	if !strings.EqualFold(resolved, defaultBaseURL) {
		const warning = "Non-default API Destination"
		detail := fmt.Sprintf("Every request, credentials included, goes to %s (set by %s) instead of %s. "+
			"base_url exists to point tests at a local server; check this is intended.",
			u.Host, source, defaultBaseURL)
		if fromConfig {
			diags.AddAttributeWarning(path.Root("base_url"), warning, detail)
		} else {
			diags.AddWarning(warning, detail)
		}
	}

	return resolved
}

// warnIfExchangeIgnoresPath: the SDK posts the federation token exchange to
// scheme://host/v1/oauth/token whatever path baseURL carries, and its
// federation option has no field to change that, while API requests do
// honour the path. Both destinations are named so the split is visible.
func warnIfExchangeIgnoresPath(baseURL string, diags *diag.Diagnostics) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Path == "" {
		return
	}
	diags.AddWarning("Base URL Path Ignored By The Token Exchange",
		fmt.Sprintf("API requests go to %s, but the workload identity federation token exchange goes to "+
			"%s://%s/v1/oauth/token: the SDK does not apply the path. Use a base URL without a path, or a static auth_token.",
			baseURL, u.Scheme, u.Host))
}
