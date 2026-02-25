APP_NAME   := pmbot
CMD_DIR    := ./cmd/pmbot
BUILD_DIR  := ./build

GOOS   := linux
GOARCH := amd64

VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.buildTime=$(BUILD_TIME)

GOFLAGS := -trimpath
UPX     := upx
UPX_FLAGS := --best --lzma

.PHONY: all build build-upx clean test lint run

all: build

build:
	@mkdir -p $(BUILD_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(APP_NAME) $(CMD_DIR)
	@echo "built: $(BUILD_DIR)/$(APP_NAME) ($(GOOS)/$(GOARCH))"

build-upx: build
	$(UPX) $(UPX_FLAGS) $(BUILD_DIR)/$(APP_NAME)
	@echo "compressed: $(BUILD_DIR)/$(APP_NAME)"

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./...

lint:
	go vet ./...

run: build
	$(BUILD_DIR)/$(APP_NAME)
