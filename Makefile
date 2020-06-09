.PHONY: vendor

BIN		?= hamsterd
PRJNAME ?= hamsterd

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

lemming: vendor
	@go build -o $(GOBIN)/lemmingd ./cmd/lemmingd/*.go
	@$(GOBIN)/lemmingd

production_lemming:
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -a -mod=vendor -o $(GOBIN)/lemmingd.linux   ./cmd/lemmingd/*.go
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -a -mod=vendor -o $(GOBIN)/lemmingd.darwin  ./cmd/lemmingd/*.go
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -a -mod=vendor -o $(GOBIN)/lemmingd.64.exe  ./cmd/lemmingd/*.go
	CGO_ENABLED=0 GOOS=windows GOARCH=386   go build -a -mod=vendor -o $(GOBIN)/lemmingd.32.exe  ./cmd/lemmingd/*.go
