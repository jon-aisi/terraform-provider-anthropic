// Copyright (c) Ippon
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
func resolveBaseURL(configValue types.String, diags *diag.Diagnostics) string {
	const summary = "Invalid Base URL"

	raw := resolveCredential(configValue, envBaseURL)
	if raw == "" {
		return defaultBaseURL
	}

	addError := func(detail string) {
		if !configValue.IsNull() && !configValue.IsUnknown() {
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

	return strings.TrimRight(raw, "/")
}
