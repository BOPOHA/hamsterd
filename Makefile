.PHONY: vendor build all

ALL_BINARY = hamsterd lemmingd
PRJNAME ?= hamsterd

GOBASE	?= $(shell pwd)
GOBIN	?= $(GOBASE)/bin

GO111MODULE = on

define build_bin_bundle
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -a -mod=vendor -ldflags="-s -w" -o $(GOBIN)/$1.linux  ./cmd/$1/*.go
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -a -mod=vendor -ldflags="-s -w" -o $(GOBIN)/$1.darwin ./cmd/$1/*.go
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -a -mod=vendor -ldflags="-s -w" -o $(GOBIN)/$1.64.exe ./cmd/$1/*.go
	CGO_ENABLED=0 GOOS=windows GOARCH=386   go build -a -mod=vendor -ldflags="-s -w" -o $(GOBIN)/$1.32.exe ./cmd/$1/*.go
endef

define build_bin_current
	go build -a -mod=vendor -o $(GOBIN)/$1  ./cmd/$1/*.go
endef

vendor:
	@go mod tidy
	@go mod vendor
	@go mod download


clean_binary:
	rm -rf $(GOBIN)

$(ALL_BINARY):
	$(call build_bin_bundle,$@)

all: clean_binary $(ALL_BINARY)

run:
	$(call build_bin_current,$(name))
	@./bin/$(name)
