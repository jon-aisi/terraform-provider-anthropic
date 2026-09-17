// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// TestMain runs the sweepers when go test is given -sweep (see make sweep)
// and the tests otherwise.
func TestMain(m *testing.M) {
	acctest.AddSweepers()
	resource.TestMain(m)
}
