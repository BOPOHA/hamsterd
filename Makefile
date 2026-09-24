.PHONY: all build clean deps hamsterd lemmingd run test vet

ALL_BINARY = hamsterd lemmingd
PRJNAME ?= hamsterd

GOBASE	?= $(shell pwd)
GOBIN	?= $(GOBASE)/bin

define build_bin_bundle
	@mkdir -p $(GOBIN)
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(GOBIN)/$1.linux-amd64 ./cmd/$1
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(GOBIN)/$1.darwin-amd64 ./cmd/$1
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(GOBIN)/$1.windows-amd64.exe ./cmd/$1
endef

define build_bin_current
	@mkdir -p $(GOBIN)
	CGO_ENABLED=0 go build -trimpath -o $(GOBIN)/$1 ./cmd/$1
endef

deps:
	go mod download

clean:
	rm -rf $(GOBIN)

$(ALL_BINARY):
	$(call build_bin_bundle,$@)

all: clean $(ALL_BINARY)

build:
	$(call build_bin_current,hamsterd)
	$(call build_bin_current,lemmingd)

test:
	CGO_ENABLED=0 go test ./...

vet:
	CGO_ENABLED=0 go vet ./...

run:
	$(call build_bin_current,$(name))
	@./bin/$(name)
