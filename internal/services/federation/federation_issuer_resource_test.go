// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

import (
	"context"
	"fmt"
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// This resource requires an org:admin OAuth bearer token (ANTHROPIC_AUTH_TOKEN);
// Admin API keys are rejected outright. No test organization or durable
// org:admin token exists in CI (see acctest.PreCheckOAuth), so these run
// locally only.
//
// jwks = {type = "inline", keys = [...]} with a key generated for the run is
// used so nothing is dialed: unlike "discovery" (which requires a publicly
// reachable HTTPS issuer_url) or "explicit_url" (which fetches the JWKS
// endpoint on every poll), "inline" keys are taken as-is and never fetched
// over the network. The private half of the key is discarded, so an issuer a
// failed run leaves behind trusts nobody.

func newTestOAuthClient() *anthropic.Client {
	return acctest.NewOAuthClient()
}

// testAccCheckFederationIssuerArchived verifies that every federation issuer
// tracked in state was archived rather than removed — the API has no
// hard-delete endpoint, so CheckDestroy must assert archived_at is set instead
// of expecting a 404.
func testAccCheckFederationIssuerArchived(s *terraform.State) error {
	client := newTestOAuthClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_federation_issuer" {
			continue
		}
		issuer, err := client.Beta.Organization.Federation.Issuers.Get(context.Background(), rs.Primary.ID, anthropic.BetaOrganizationFederationIssuerGetParams{})
		if err != nil {
			return fmt.Errorf("unable to read federation issuer %s: %w", rs.Primary.ID, err)
		}
		if issuer.ArchivedAt.IsZero() {
			return fmt.Errorf("federation issuer %s was not archived on destroy", rs.Primary.ID)
		}
	}
	return nil
}

func testAccFederationIssuerInlineConfig(name, issuerURL, keysJSON string) string {
	return fmt.Sprintf(`
resource "anthropic_federation_issuer" "test" {
  name       = %[1]q
  issuer_url = %[2]q

  jwks = {
    type = "inline"
    keys = %[3]q
  }

  check_jti                = false
  max_jwt_lifetime_seconds = 900
}
`, name, issuerURL, keysJSON)
}

func TestAccFederationIssuerResource_inline(t *testing.T) {
	name := acctest.RandomName("issuer")
	issuerURL := fmt.Sprintf("https://%s.example.com", acctest.RandomName("idp"))
	keys := acctest.FreshRSAJWKJSON(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckOAuth(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckFederationIssuerArchived,
		Steps: []resource.TestStep{
			{
				Config: testAccFederationIssuerInlineConfig(name, issuerURL, keys),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_federation_issuer.test", "id"),
					resource.TestCheckResourceAttr("anthropic_federation_issuer.test", "name", name),
					resource.TestCheckResourceAttr("anthropic_federation_issuer.test", "issuer_url", issuerURL),
					resource.TestCheckResourceAttr("anthropic_federation_issuer.test", "jwks.type", "inline"),
					resource.TestCheckResourceAttr("anthropic_federation_issuer.test", "check_jti", "false"),
					resource.TestCheckResourceAttr("anthropic_federation_issuer.test", "max_jwt_lifetime_seconds", "900"),
					resource.TestCheckResourceAttrSet("anthropic_federation_issuer.test", "created_at"),
					resource.TestCheckResourceAttrSet("anthropic_federation_issuer.test", "created_by_actor_id"),
					resource.TestCheckNoResourceAttr("anthropic_federation_issuer.test", "archived_at"),
				),
			},
			{
				// Update: rename only.
				Config: testAccFederationIssuerInlineConfig(name+"-renamed", issuerURL, keys),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_federation_issuer.test", "name", name+"-renamed"),
					resource.TestCheckResourceAttr("anthropic_federation_issuer.test", "jwks.type", "inline"),
				),
			},
			{
				ResourceName:      "anthropic_federation_issuer.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
