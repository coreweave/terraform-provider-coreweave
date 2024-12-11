default: fmt lint install generate

build:
	go build -v ./...

# Variables
PROVIDER_NAME := coreweave
VERSION := 1.0.0
PLUGIN_DIR := $(HOME)/.terraform.d/plugins/terraform.local/coreweave/$(PROVIDER_NAME)/$(VERSION)/linux_amd64
BINARY_NAME := terraform-provider-$(PROVIDER_NAME)_v$(VERSION)

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

testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

.PHONY: fmt lint test testacc build install generate clean
