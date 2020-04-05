.PHONY: vendor

BIN		?= hamsterd
PRJNAME ?= hamster

GOBASE	?= $(shell pwd)
GOBIN	?= $(GOBASE)/bin

GO111MODULE = on

vendor:
	@go mod vendor

build:
	@go build -o $(GOBIN)/$(BIN) ./cmd/$(PRJNAME)/main.go || exit

production:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags="-s -w" -o $(GOBIN)/$(BIN) ./cmd/$(PRJNAME)/main.go

start: build
	@./bin/$(BIN)

