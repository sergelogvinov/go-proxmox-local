
TESTARGS ?= "-v"

############

# Help Menu

define HELP_MENU_HEADER
# Getting Started

To build this project, you must have the following installed:

- git
- make
- golang 1.27+
- golangci-lint

endef

export HELP_MENU_HEADER

help: ## This help menu
	@echo "$$HELP_MENU_HEADER"
	@grep -E '^[a-zA-Z0-9%_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

############
#
# Build Abstractions
#

.PHONY: lint
lint: ## Lint Code
	golangci-lint run --config .golangci.yml

.PHONY: unit
unit: ## Unit Tests
	go test -tags=unit $(shell go list ./...) $(TESTARGS)

.PHONY: test
test: lint unit ## Run all tests

.PHONY: licenses
licenses:
	go-licenses check ./... --disallowed_types=forbidden,restricted,unknown

.PHONY: conformance
conformance: ## Conformance
	docker run --rm -it -v $(PWD):/src -w /src ghcr.io/siderolabs/conform:v0.1.0-alpha.31 enforce
