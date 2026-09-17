// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package acctest holds what the TestAcc* functions share: the provider
// factory, the environment pre-checks, the organisation guard, fixture
// helpers and the sweepers.
//
// The acceptance tests create, update and archive federation issuers, rules
// and service accounts in whatever organisation the credential belongs to.
// Every pre-check therefore refuses to run unless ANTHROPIC_TEST_ORGANIZATION_ID
// is set and matches the organisation the API reports for the credential.
package acctest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/provider"
)

// Environment variables the acceptance tests read. The first three are the
// provider's own; the last two exist only for the tests.
const (
	EnvAuthToken   = "ANTHROPIC_AUTH_TOKEN"
	EnvAdminAPIKey = "ANTHROPIC_ADMIN_API_KEY"
	EnvBaseURL     = "ANTHROPIC_BASE_URL"
	// EnvTestOrganizationID names the organisation the tests may write to.
	// Every pre-check compares it with the organisation the credential
	// resolves to and fails on a mismatch.
	EnvTestOrganizationID = "ANTHROPIC_TEST_ORGANIZATION_ID"
	// EnvTestWorkspaceID is a workspace in that organisation for the tests
	// that bind a rule or a service account to one.
	EnvTestWorkspaceID = "ANTHROPIC_TEST_WORKSPACE_ID"
)

// ResourceNamePrefix starts the name of every object the acceptance tests
// create. The sweepers archive by it, so nothing else in the test
// organisation may carry it.
const ResourceNamePrefix = "tf-acc-"

// ProtoV6ProviderFactories is used to instantiate a provider during acceptance testing.
var ProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"anthropic": providerserver.NewProtocol6WithError(provider.New("test")()),
}

// RandomName returns "tf-acc-<kind>-<8 hex digits>", a valid slug for every
// WIF object name, unique across runs so a leftover never collides with the
// next run's object (names are unique per organisation; a duplicate is a 409).
func RandomName(kind string) string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("acctest: reading random bytes: %s", err))
	}
	return fmt.Sprintf("%s%s-%s", ResourceNamePrefix, kind, hex.EncodeToString(b))
}

// skipUnlessAcceptance skips the test when TF_ACC is unset, the same check
// resource.Test makes. The fixture helpers and pre-checks run before
// resource.Test, so without it a plain `go test ./...` with credentials in
// the environment would create live objects and then skip.
func skipUnlessAcceptance(t *testing.T) {
	t.Helper()
	if os.Getenv(resource.EnvTfAcc) == "" {
		t.Skipf("acceptance tests skipped unless env '%s' set", resource.EnvTfAcc)
	}
}

// TestWorkspaceID returns EnvTestWorkspaceID or fails the test.
func TestWorkspaceID(t *testing.T) string {
	t.Helper()
	skipUnlessAcceptance(t)
	v := os.Getenv(EnvTestWorkspaceID)
	if v == "" {
		t.Fatalf("%s must be set to a workspace in the test organisation", EnvTestWorkspaceID)
	}
	return v
}

// PreCheckOAuth gates tests that hit endpoints requiring an org:admin OAuth
// bearer token (the Workload Identity Federation admin endpoints reject API
// keys). It skips without a token, so a plain `make testacc` on a machine
// without one runs only the unit tests, and fails unless the token belongs
// to the organisation named by EnvTestOrganizationID.
func PreCheckOAuth(t *testing.T) {
	t.Helper()
	skipUnlessAcceptance(t)
	if os.Getenv(EnvAuthToken) == "" {
		t.Skipf("%s must be set for OAuth acceptance tests; skipping", EnvAuthToken)
	}
	requireTestOrganization(t, EnvAuthToken, &oauthOrganization, oauthOrganizationID)
}

// PreCheckAdmin gates tests that hit the Admin API with an Admin API key.
func PreCheckAdmin(t *testing.T) {
	t.Helper()
	skipUnlessAcceptance(t)
	if os.Getenv(EnvAdminAPIKey) == "" {
		t.Fatalf("%s must be set for admin acceptance tests", EnvAdminAPIKey)
	}
	requireTestOrganization(t, EnvAdminAPIKey, &adminOrganization, adminOrganizationID)
}

// NewOAuthClient builds the SDK client the tests use for fixtures and
// post-destroy checks: the bearer from EnvAuthToken, the base URL from
// EnvBaseURL when set, and nothing else from the environment, so an ambient
// profile or ANTHROPIC_API_KEY cannot redirect it.
func NewOAuthClient() *anthropic.Client {
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithAuthToken(os.Getenv(EnvAuthToken)),
	}
	if base := os.Getenv(EnvBaseURL); base != "" {
		opts = append(opts, option.WithBaseURL(base))
	}
	c := anthropic.NewClient(opts...)
	return &c
}

// FreshRSAJWK generates a 2048-bit RSA key and returns its public half as a
// JWK for an inline issuer. The private half lives only in this process and
// is discarded: the tests never exchange a token, they only need a key the
// API accepts. The RFC 7517 example key this replaces has a published private
// half, so an issuer left behind with it would trust anyone.
func FreshRSAJWK(t *testing.T) map[string]any {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating an RSA key: %s", err)
	}
	enc := base64.RawURLEncoding
	return map[string]any{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": RandomName("key"),
		"n":   enc.EncodeToString(key.N.Bytes()),
		"e":   enc.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
}

// FreshRSAJWKJSON is FreshRSAJWK as the JSON array the inline `jwks.keys`
// attribute takes.
func FreshRSAJWKJSON(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal([]map[string]any{FreshRSAJWK(t)})
	if err != nil {
		t.Fatalf("encoding the JWK: %s", err)
	}
	return string(b)
}

// --- organisation guard ---

// organizationLookup caches one credential's organisation for the process:
// every TestAcc* pre-check calls it, and the answer does not change.
type organizationLookup struct {
	once sync.Once
	id   string
	err  error
}

var (
	oauthOrganization organizationLookup
	adminOrganization organizationLookup
)

func (l *organizationLookup) get(fetch func(context.Context) (string, error)) (string, error) {
	l.once.Do(func() { l.id, l.err = fetch(context.Background()) })
	return l.id, l.err
}

// requireTestOrganization fails the test unless EnvTestOrganizationID is set
// and equals the organisation the credential resolves to.
func requireTestOrganization(t *testing.T, credential string, lookup *organizationLookup, fetch func(context.Context) (string, error)) {
	t.Helper()
	want := os.Getenv(EnvTestOrganizationID)
	if want == "" {
		t.Fatalf("%s must be set: the acceptance tests create and archive objects in whatever organisation %s belongs to", EnvTestOrganizationID, credential)
	}
	got, err := lookup.get(fetch)
	if err != nil {
		t.Fatalf("resolving the organisation %s belongs to: %s", credential, err)
	}
	if got != want {
		t.Fatalf("%s belongs to organisation %s, not the test organisation %s named by %s; refusing to run", credential, got, want, EnvTestOrganizationID)
	}
}

func oauthOrganizationID(ctx context.Context) (string, error) {
	org, err := NewOAuthClient().Beta.Organization.Get(ctx)
	if err != nil {
		return "", err
	}
	if org.ID == "" {
		return "", errors.New("GET /v1/organizations/me returned no id")
	}
	return org.ID, nil
}

func adminOrganizationID(ctx context.Context) (string, error) {
	client := admin.NewClient(os.Getenv(EnvAdminAPIKey))
	if base := os.Getenv(EnvBaseURL); base != "" {
		client.BaseURL = strings.TrimSuffix(base, "/")
	}
	body, err := client.DoRequest(ctx, "GET", "/v1/organizations/me", nil)
	if err != nil {
		return "", err
	}
	var org struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &org); err != nil {
		return "", fmt.Errorf("decoding GET /v1/organizations/me: %w", err)
	}
	if org.ID == "" {
		return "", errors.New("GET /v1/organizations/me returned no id")
	}
	return org.ID, nil
}

// --- sweepers ---

// AddSweepers registers the sweepers that archive objects named with
// ResourceNamePrefix, for runs a failed test or a killed process left behind.
// Each package's TestMain calls it and hands off to resource.TestMain; run
// with `make sweep` (go test <package> -sweep=all). Rules go first: the API
// refuses to archive an issuer or a service account a live rule references.
// The sweepers apply the same organisation guard as the pre-checks.
func AddSweepers() {
	resource.AddTestSweepers("anthropic_federation_rule", &resource.Sweeper{
		Name: "anthropic_federation_rule",
		F:    sweepFederationRules,
	})
	resource.AddTestSweepers("anthropic_federation_issuer", &resource.Sweeper{
		Name:         "anthropic_federation_issuer",
		Dependencies: []string{"anthropic_federation_rule"},
		F:            sweepFederationIssuers,
	})
	resource.AddTestSweepers("anthropic_service_account", &resource.Sweeper{
		Name:         "anthropic_service_account",
		Dependencies: []string{"anthropic_federation_rule"},
		F:            sweepServiceAccounts,
	})
}

// sweeperClient returns the OAuth client once the credential is confirmed to
// belong to the test organisation.
func sweeperClient(ctx context.Context) (*anthropic.Client, error) {
	if os.Getenv(EnvAuthToken) == "" {
		return nil, fmt.Errorf("%s is not set", EnvAuthToken)
	}
	want := os.Getenv(EnvTestOrganizationID)
	if want == "" {
		return nil, fmt.Errorf("%s is not set; refusing to sweep", EnvTestOrganizationID)
	}
	got, err := oauthOrganization.get(oauthOrganizationID)
	if err != nil {
		return nil, fmt.Errorf("resolving the organisation %s belongs to: %w", EnvAuthToken, err)
	}
	if got != want {
		return nil, fmt.Errorf("%s belongs to organisation %s, not the test organisation %s; refusing to sweep", EnvAuthToken, got, want)
	}
	return NewOAuthClient(), nil
}

func isSweepable(name string, archivedAt interface{ IsZero() bool }) bool {
	return strings.HasPrefix(name, ResourceNamePrefix) && archivedAt.IsZero()
}

func sweepFederationRules(_ string) error {
	ctx := context.Background()
	client, err := sweeperClient(ctx)
	if err != nil {
		return err
	}
	iter := client.Beta.Organization.Federation.Rules.ListAutoPaging(ctx, anthropic.BetaOrganizationFederationRuleListParams{})
	for iter.Next() {
		rule := iter.Current()
		if !isSweepable(rule.Name, rule.ArchivedAt) {
			continue
		}
		log.Printf("[INFO] sweeper: archiving federation rule %s (%s)", rule.Name, rule.ID)
		if _, err := client.Beta.Organization.Federation.Rules.Archive(ctx, rule.ID, anthropic.BetaOrganizationFederationRuleArchiveParams{}); err != nil {
			return fmt.Errorf("archiving federation rule %s: %w", rule.ID, err)
		}
	}
	return iter.Err()
}

func sweepFederationIssuers(_ string) error {
	ctx := context.Background()
	client, err := sweeperClient(ctx)
	if err != nil {
		return err
	}
	iter := client.Beta.Organization.Federation.Issuers.ListAutoPaging(ctx, anthropic.BetaOrganizationFederationIssuerListParams{})
	for iter.Next() {
		issuer := iter.Current()
		if !isSweepable(issuer.Name, issuer.ArchivedAt) {
			continue
		}
		log.Printf("[INFO] sweeper: archiving federation issuer %s (%s)", issuer.Name, issuer.ID)
		if _, err := client.Beta.Organization.Federation.Issuers.Archive(ctx, issuer.ID, anthropic.BetaOrganizationFederationIssuerArchiveParams{}); err != nil {
			return fmt.Errorf("archiving federation issuer %s: %w", issuer.ID, err)
		}
	}
	return iter.Err()
}

func sweepServiceAccounts(_ string) error {
	ctx := context.Background()
	client, err := sweeperClient(ctx)
	if err != nil {
		return err
	}
	iter := client.Beta.Organization.ServiceAccounts.ListAutoPaging(ctx, anthropic.BetaOrganizationServiceAccountListParams{})
	for iter.Next() {
		account := iter.Current()
		if !isSweepable(account.Name, account.ArchivedAt) {
			continue
		}
		log.Printf("[INFO] sweeper: archiving service account %s (%s)", account.Name, account.ID)
		if _, err := client.Beta.Organization.ServiceAccounts.Archive(ctx, account.ID, anthropic.BetaOrganizationServiceAccountArchiveParams{}); err != nil {
			return fmt.Errorf("archiving service account %s: %w", account.ID, err)
		}
	}
	return iter.Err()
}
