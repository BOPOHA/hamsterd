.PHONY: vendor

BIN		?= hamsterd
PRJNAME ?= hamster

GOBASE	?= $(shell pwd)
GOBIN	?= $(GOBASE)/bin

vendor:
	@go mod vendor

build:
	@go build -o $(GOBIN)/$(BIN) ./cmd/$(PRJNAME)/main.go || exit

production:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(GOBIN)/$(BIN) ./cmd/$(PRJNAME)/main.go

start: build
	@./bin/$(BIN)

