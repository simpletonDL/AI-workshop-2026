BINARY := atlas
BIN_DIR := bin

.PHONY: build install test test-integration test-ui

build:
	go build -o $(BIN_DIR)/$(BINARY) ./cmd/atlas

install:
	go install ./cmd/atlas

test:
	go test ./...

test-integration:
	go test -tags integration -count=1 ./test/integration/

test-ui:
	scripts/ui-test.sh
