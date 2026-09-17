// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// addArchivedOutsideTerraformWarning reports an object the API returns as
// archived while Terraform still manages it. Read keeps the object in state on
// purpose: RemoveResource would make the next plan re-create it, which for a
// federation rule or issuer means re-granting access that was revoked in the
// Console.
func addArchivedOutsideTerraformWarning(diags *diag.Diagnostics, kind, name, id, archivedAt string) {
	diags.AddWarning(
		kind+" archived outside Terraform",
		fmt.Sprintf("%s %q (%s) was archived outside Terraform at %s. Further updates will fail; remove it from configuration or re-create it under a new name.",
			kind, name, id, archivedAt),
	)
}
