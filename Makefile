BINARY_NAME := funaid
P2P_BINARY := funai-node
BUILD_DIR := ./build
GO := go
GOFLAGS := -mod=readonly
LDFLAGS := -s -w

.PHONY: all build build-p2p build-e2e-client build-all install clean test test-e2e test-e2e-real test-p2p-e2e-mock bench lint proto \
        init start testnet-init testnet-clean docker-build

all: build-all

build:
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/funaid

build-p2p:
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(P2P_BINARY) ./cmd/funai-node

build-e2e-client:
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/e2e-client ./cmd/e2e-client

build-all: build build-p2p build-e2e-client

install:
	$(GO) install $(GOFLAGS) -ldflags "$(LDFLAGS)" ./cmd/funaid
	$(GO) install $(GOFLAGS) -ldflags "$(LDFLAGS)" ./cmd/funai-node

clean:
	rm -rf $(BUILD_DIR)

test:
	$(GO) test ./... -race -coverprofile=coverage.out

bench:
	$(GO) test ./bench/... -bench=. -benchtime=10s -benchmem -run=^$

lint:
	golangci-lint run ./...

proto:
	@echo "Generating protobuf files..."
	buf generate

init:
	./$(BUILD_DIR)/$(BINARY_NAME) init funai-node --chain-id funai-1
	./$(BUILD_DIR)/$(BINARY_NAME) keys add validator --keyring-backend test
	./$(BUILD_DIR)/$(BINARY_NAME) genesis add-genesis-account validator 100000000000ufai --keyring-backend test
	./$(BUILD_DIR)/$(BINARY_NAME) genesis gentx validator 1000000ufai --chain-id funai-1 --keyring-backend test
	./$(BUILD_DIR)/$(BINARY_NAME) genesis collect-gentxs

start:
	./$(BUILD_DIR)/$(BINARY_NAME) start

testnet-init:
	BINARY=$(BUILD_DIR)/$(BINARY_NAME) bash scripts/init-testnet.sh

testnet-clean:
	rm -rf $(HOME)/.funai-testnet

test-e2e: build
	bash scripts/e2e-test.sh

test-e2e-real: build-all
	bash scripts/e2e-real-inference.sh

test-p2p-e2e-mock: build-all
	bash scripts/e2e-mock-inference.sh

docker-build:
	docker build --target funaid -t funai-chain:latest .
	docker build --target funai-node -t funai-p2p:latest .
