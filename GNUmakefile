default: fmt lint install generate

build:
	go build -v ./...

# Variables
PROVIDER_NAME := coreweave
VERSION := 1.0.0
PLUGIN_DIR := $(HOME)/.terraform.d/plugins/terraform.local/coreweave/$(PROVIDER_NAME)/$(VERSION)/linux_amd64
BINARY_NAME := terraform-provider-$(PROVIDER_NAME)_v$(VERSION)

# This is valuable for limiting the sweeps to known-good resources, and for forcing an ordering.
TEST_ACC_PACKAGES=./coreweave/cks ./coreweave/networking
TEST_ACC_SWEEP_ZONE=US-EAST-04A

# Build and install the provider binary
install: clean
	mkdir -p $(PLUGIN_DIR)
	go build -o $(PLUGIN_DIR)/$(BINARY_NAME)
	chmod +x $(PLUGIN_DIR)/$(BINARY_NAME)
	@echo "Provider binary installed at $(PLUGIN_DIR)/$(BINARY_NAME)"

# Clean up the generated binary
clean:
	rm -f $(PLUGIN_DIR)/$(BINARY_NAME)
	@echo "Cleaned up $(PLUGIN_DIR)/$(BINARY_NAME)"

lint:
	golangci-lint run

generate:
	cd tools; go generate ./...

fmt:
	gofmt -s -w -e .

test:
	go test -v -cover -timeout=120s -parallel=10 ./...

testacc-sweep:
	go test -v -timeout 60m $(TEST_ACC_PACKAGES) -sweep='$(TEST_ACC_SWEEP_ZONE)'

testacc:
	TF_ACC=1 go test -v -cover -timeout=60m $(TEST_ACC_PACKAGES)

.PHONY: fmt lint test testacc build install generate clean
