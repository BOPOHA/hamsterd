# SPDX-License-Identifier: MIT
%global debug_package %{nil}

Name:           hamsterd
Version:        0.1.0
Release:        1%{?dist}
Summary:        Local caching HTTP and HTTPS development proxy

License:        MIT AND BSD-3-Clause
URL:            https://github.com/BOPOHA/hamsterd
Source0:        {{{ git_repo_pack }}}
Source1:        {{{ go_vendor_pack }}}

BuildRequires:  golang >= 1.25
BuildRequires:  gcc

# All linked modules are carried in Source1 for reproducible offline builds.
Provides:       bundled(golang(github.com/elazarl/goproxy)) = 1.9.1
Provides:       bundled(golang(golang.org/x/net)) = 0.51.0
Provides:       bundled(golang(golang.org/x/text)) = 0.34.0

%description
hamsterd is an HTTP and HTTPS proxy that listens on the loop-back interface by
default. It caches eligible public downloads to speed up repeated development
and package builds. It creates a unique local certificate authority and never
installs trust automatically.

%package -n lemmingd
Summary:        Selective local-development HTTP and HTTPS proxy
License:        MIT AND BSD-3-Clause
Provides:       bundled(golang(github.com/elazarl/goproxy)) = 1.9.1
Provides:       bundled(golang(golang.org/x/net)) = 0.51.0
Provides:       bundled(golang(golang.org/x/text)) = 0.34.0

%description -n lemmingd
lemmingd intercepts configured development domains and routes selected URL paths
to local HTTP servers. Other domains are passed through without TLS
interception.

%prep
%setup -T -b 0 -q -n hamsterd
tar xzf %{SOURCE1}

%build
CGO_ENABLED=1 go build -mod=vendor -buildmode=pie -trimpath \
    -ldflags "-linkmode=external -s -w -X github.com/BOPOHA/hamsterd/internal/buildinfo.Version=%{version}" \
    -o %{_builddir}/hamsterd-bin ./cmd/hamsterd
CGO_ENABLED=1 go build -mod=vendor -buildmode=pie -trimpath \
    -ldflags "-linkmode=external -s -w -X github.com/BOPOHA/hamsterd/internal/buildinfo.Version=%{version}" \
    -o %{_builddir}/lemmingd-bin ./cmd/lemmingd

%check
CGO_ENABLED=0 go test -mod=vendor ./...

%install
install -Dm755 %{_builddir}/hamsterd-bin %{buildroot}%{_bindir}/hamsterd
install -Dm755 %{_builddir}/lemmingd-bin %{buildroot}%{_bindir}/lemmingd
install -Dm644 docs/hamsterd.1 %{buildroot}%{_mandir}/man1/hamsterd.1
install -Dm644 docs/lemmingd.1 %{buildroot}%{_mandir}/man1/lemmingd.1
for package in hamsterd lemmingd; do
    install -Dm644 LICENSE %{buildroot}%{_licensedir}/${package}/LICENSE
    install -Dm644 vendor/github.com/elazarl/goproxy/LICENSE \
        %{buildroot}%{_licensedir}/${package}/LICENSE-goproxy
    # x/net and x/text carry the same Go project BSD-3-Clause text.
    install -Dm644 vendor/golang.org/x/net/LICENSE \
        %{buildroot}%{_licensedir}/${package}/LICENSE-go-x-net-and-x-text
done

%files
%license %{_licensedir}/hamsterd/
%doc README.md SECURITY.md CHANGELOG.md examples/hamsterd.json
%{_bindir}/hamsterd
%{_mandir}/man1/hamsterd.1*

%files -n lemmingd
%license %{_licensedir}/lemmingd/
%doc README.md SECURITY.md CHANGELOG.md examples/lemmingd.json
%{_bindir}/lemmingd
%{_mandir}/man1/lemmingd.1*

%changelog
* Fri Sep 25 2026 Anatolii Vorona <vorona.tolik@gmail.com> - 0.1.0-1
- Modernize the proxy core, certificate handling, cache, tests, and packaging
- Build hamsterd and lemmingd as separate packages from one source RPM
