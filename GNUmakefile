default: fmt lint install generate

build:
	go build -v ./...

# Variables
PROVIDER_NAME := coreweave
VERSION := 1.0.0
OS := $(shell go env GOOS)
ARCH := $(shell go env GOARCH)
PLUGIN_DIR := $(HOME)/.terraform.d/plugins/terraform.local/coreweave/$(PROVIDER_NAME)/$(VERSION)/$(OS)_$(ARCH)
BINARY_NAME := terraform-provider-$(PROVIDER_NAME)_v$(VERSION)

# This is valuable for limiting the sweeps to known-good resources, and for forcing an ordering.
TEST_ACC_PACKAGES?=./coreweave/cks ./coreweave/networking
TEST_ACC_SWEEP_ZONE?=US-LAB-01A

export CGO_ENABLED?=0

# Build and install the provider binary
install: clean
	mkdir -p $(PLUGIN_DIR)
	go build -o $(PLUGIN_DIR)/$(BINARY_NAME)
	chmod +x $(PLUGIN_DIR)/$(BINARY_NAME)
	@echo "Provider binary installed at $(PLUGIN_DIR)/$(BINARY_NAME)"

debug:
	go build -gcflags=all='-N -l' -o __debug_bin_manual . && dlv exec --accept-multiclient --continue --headless ./__debug_bin_manual -- -debug

# Clean up the generated binary
clean:
	rm -f $(PLUGIN_DIR)/$(BINARY_NAME) ./__debug_*
	@echo "Cleaned up $(PLUGIN_DIR)/$(BINARY_NAME)"

lint:
	golangci-lint run

generate:
	cd tools; go generate ./...

fmt:
	gofmt -s -w -e .

test:
	go test -v -cover -timeout=120s -parallel=10 ./...

SUITES?=cks networking

testacc-sweep:
	@for suite in $(SUITES); do \
		go test -v -timeout 10m ./coreweave/$$suite -sweep='$(TEST_ACC_SWEEP_ZONE)'; \
	done

testacc:
	@for suite in $(SUITES); do \
		TF_ACC=1 go test -v -cover -timeout=45m ./coreweave/$$suite; \
	done

.PHONY: debug fmt lint test testacc testacc-sweep build install generate clean
