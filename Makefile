BINARY_NAME=mdt
BUILD_DIR=build

.PHONY: all build test coverage clean install vet fmt

all: vet test build

build:
	mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/mdt

test:
	go test -v ./...

coverage:
	go test -cover -coverprofile=coverage.out ./test/... -coverpkg=./internal/...
	go tool cover -func=coverage.out

vet:
	go vet ./...

fmt:
	go fmt ./...

clean:
	rm -rf $(BUILD_DIR)
	rm -f coverage.out

install:
	go install ./cmd/mdt
