.PHONY: all build builddep clean deps fc hamsterd lemmingd rpm run srpm test vet

ALL_BINARY = hamsterd lemmingd
PRJNAME ?= hamsterd
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

GOBASE	?= $(shell pwd)
GOBIN	?= $(GOBASE)/bin
RPM_OUTDIR ?= $(GOBASE)/rpmbuild
SPEC := packaging/hamsterd.spec
FEDORA_VERSION ?= $(shell rpm -E %fedora)
MOCK_CONFIG ?= fedora-$(FEDORA_VERSION)-$(shell uname -m)
LDFLAGS := -s -w \
	-X github.com/BOPOHA/hamsterd/internal/buildinfo.Version=$(VERSION)

define build_bin_bundle
	@mkdir -p $(GOBIN)
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o $(GOBIN)/$1.linux-amd64 ./cmd/$1
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o $(GOBIN)/$1.darwin-amd64 ./cmd/$1
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o $(GOBIN)/$1.windows-amd64.exe ./cmd/$1
endef

define build_bin_current
	@mkdir -p $(GOBIN)
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(GOBIN)/$1 ./cmd/$1
endef

deps:
	go mod download

clean:
	@for path in \
		"$(GOBIN)" \
		"$(GOBASE)/dist" \
		"$(RPM_OUTDIR)" \
		"$(GOBASE)/rpm-results" \
		"$(GOBASE)/vendor"; do \
		case "$$path" in \
			"$(GOBASE)"/*) rm -rf -- "$$path" ;; \
			*) echo "refusing to clean path outside $(GOBASE): $$path" >&2; exit 1 ;; \
		esac; \
	done
	@find "$(GOBASE)" -maxdepth 1 -type f \
		\( -name '*.test' -o -name '*.out' \) -delete

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

srpm:
	mkdir -p $(RPM_OUTDIR)/SRPMS
	rm -f $(RPM_OUTDIR)/SRPMS/*.src.rpm
	rpkg srpm --spec $(SPEC) --outdir $(RPM_OUTDIR)/SRPMS
	@find $(RPM_OUTDIR)/SRPMS -name '*.src.rpm' -print

rpm: srpm
	@srpm="$$(find $(RPM_OUTDIR)/SRPMS -name '*.src.rpm' -print -quit)"; \
		test -n "$$srpm"; \
		rpmbuild --rebuild "$$srpm" --define "_topdir $(RPM_OUTDIR)"
	@find $(RPM_OUTDIR)/RPMS -name '*.rpm' -print

builddep: srpm
	@srpm="$$(find $(RPM_OUTDIR)/SRPMS -name '*.src.rpm' -print -quit)"; \
		test -n "$$srpm"; \
		dnf builddep --assumeno "$$srpm"

fc: srpm
	@srpm="$$(find $(RPM_OUTDIR)/SRPMS -name '*.src.rpm' -print -quit)"; \
		test -n "$$srpm"; \
		mock --no-clean -r $(MOCK_CONFIG) --resultdir=$(GOBASE)/rpm-results "$$srpm"

run:
	$(call build_bin_current,$(name))
	@./bin/$(name)
