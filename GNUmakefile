default: fmt tidy-check lint test install generate

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	golangci-lint run

generate:
	cd tools; go generate ./...

# Files, not directories: gofmt recurses into a directory, and the root
# package's directory contains vendor/, which is committed as go mod vendor
# wrote it.
fmt:
	find . -path ./vendor -prune -o -name '*.go' -print0 | xargs -0 gofmt -s -w -e

# `-diff` reports what `go mod tidy` would change and exits non-zero, without
# touching go.mod/go.sum — so the check is safe to run on a dirty worktree and
# needs no `git diff --exit-code` follow-up (Go 1.23+).
tidy-check:
	@go mod tidy -diff || \
		(echo; echo "go.mod/go.sum are not tidy. Run 'go mod tidy' and commit the result."; exit 1)

test:
	go test -v -cover -timeout=120s -parallel=10 ./...

# The TestAcc* functions create and archive objects in the organisation the
# credential belongs to. They refuse to run unless ANTHROPIC_TEST_ORGANIZATION_ID
# names that organisation; ANTHROPIC_TEST_WORKSPACE_ID is a workspace in it.
# See internal/acctest.
testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

# Archives every federation rule, issuer and service account named tf-acc-*
# that an interrupted acceptance run left behind. Same organisation guard as
# the tests.
sweep:
	go test -v -timeout 10m ./internal/services/federation ./internal/services/serviceaccounts -sweep=all

.dev.tfrc:
	@GOBIN=$$(go env GOBIN); \
	printf 'provider_installation {\n  dev_overrides {\n    "registry.terraform.io/ippontech/anthropic" = "%s"\n  }\n  direct {}\n}\n' \
		"$${GOBIN:-$$(go env GOPATH)/bin}" > $@

# tests/ is the root module: it holds versions.tf (the provider source mapping
# the test files' provider/mock_provider blocks resolve against) so the
# repository root stays free of Terraform configuration.
terraform-test: install .dev.tfrc
	TF_CLI_CONFIG_FILE=$(CURDIR)/.dev.tfrc terraform -chdir=tests init
	TF_CLI_CONFIG_FILE=$(CURDIR)/.dev.tfrc terraform -chdir=tests test

.PHONY: fmt tidy-check lint test testacc sweep terraform-test build install generate
