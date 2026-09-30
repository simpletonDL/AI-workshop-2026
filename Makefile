BINARY := atlas
BIN_DIR := bin

.PHONY: build install test

build:
	go build -o $(BIN_DIR)/$(BINARY) ./cmd/atlas

install:
	go install ./cmd/atlas

test:
	go test ./...
