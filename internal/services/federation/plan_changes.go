// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import "github.com/hashicorp/terraform-plugin-framework/attr"

// planChanges reports whether a planned value differs from the prior state.
// An unknown plan value (an unresolved reference) is not reported: the
// ModifyPlan warnings are about changes the practitioner can review in the
// plan output, and a value that only resolves at apply time cannot be.
func planChanges(plan, state attr.Value) bool {
	return !plan.IsUnknown() && !plan.Equal(state)
}
