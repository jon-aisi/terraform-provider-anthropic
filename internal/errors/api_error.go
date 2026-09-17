// SPDX-License-Identifier: MPL-2.0

package errors

import (
	stderrors "errors"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

// Detail returns err's message for use as a diagnostic detail. An SDK
// *anthropic.Error prints the whole response body after the method, URL,
// status and request id; here that body is capped at admin.MaxErrorBodyBytes
// and everything else, wrapping text included, is kept. The admin client's
// APIError is capped when it is built, and any other error is returned as is.
func Detail(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()

	var apiErr *anthropic.Error
	if stderrors.As(err, &apiErr) {
		if raw := apiErr.RawJSON(); len(raw) > admin.MaxErrorBodyBytes {
			msg = strings.Replace(msg, raw, admin.TruncateBody(raw), 1)
		}
	}
	return msg
}
