# hamsterd and lemmingd

[![CI](https://github.com/BOPOHA/hamsterd/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/BOPOHA/hamsterd/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/BOPOHA/hamsterd)](https://github.com/BOPOHA/hamsterd/releases/latest)
[![Go Report Card](https://goreportcard.com/badge/github.com/BOPOHA/hamsterd)](https://goreportcard.com/report/github.com/BOPOHA/hamsterd)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Two small, single-binary HTTPS development proxies:**

- **`hamsterd`** downloads a build artifact once and serves it from cache to
  every later build, container, CI runner, or team machine. A Terragrunt run
  went from **2m 5.8s to 21.4s**, almost six times faster.
- **`lemmingd`** serves your local `npm run dev` frontend, API, or mock under a
  real site's HTTPS URL, while every other path keeps using the real server.
  There is no build, deployment, or preview environment.

<!--
Demo recordings: run `make demo` (requires https://github.com/charmbracelet/vhs)
and add docs/demo/hamsterd.gif and docs/demo/lemmingd.gif here.
-->

```sh
# hamsterd: cache downloads for any tool that honours HTTPS_PROXY
hamsterd &
export HTTPS_PROXY=http://127.0.0.1:8080 HTTP_PROXY=http://127.0.0.1:8080
# trust ~/.config/hamsterd/ca.crt in the client, then build as usual

# lemmingd: add a rule such as "app.example.com, /  ->  127.0.0.1:5173" to
# ~/.config/lemmingd/config.json, then open the real site in an isolated Firefox
lemmingd firefox https://app.example.com/
```

Both listen on loopback by default and generate a unique local CA on first
start, which they never install into a system trust store. HTTPS stays verified
upstream, and only explicitly configured hosts or all hosts (hamsterd's choice)
are intercepted.

| Command | Purpose | HTTPS behavior |
| --- | --- | --- |
| `hamsterd` | Download once; reuse across builds, ephemeral environments, or a controlled development network. | Intercepts all HTTPS hosts by default, or only an optional exact-host allowlist. |
| `lemmingd` | Replace selected routes of a real site with a local frontend, API, mock, asset server, or alternate hosting origin. | Intercepts configured domains only; other CONNECT traffic is tunneled unchanged. |

The optional `lemmingd firefox` command imports the public CA only into its
dedicated Firefox profile.

## What they solve

### Download once, reuse anywhere in development

A clean build, disposable CI runner, container, or VM may download the same
provider, SDK, browser, compiler, or test fixture every time. Tool-specific
filesystem caches solve only one tool at a time and are often discarded with
the environment. `hamsterd` caches at the HTTP layer, so multiple tools and
machines can reuse the same eligible public response.

In the measured Terragrunt example, a run that took **2m 5.839s** without the
proxy took **21.394s** with a warm `hamsterd` cache—almost six times faster.
See the [complete benchmark and setup](docs/hamsterd.howto.md).

The default is a private, loopback-only cache. It can also serve trusted
clients on a controlled development network, so repeated requests for a usable
cache entry stay on the local network. That mode requires explicit
configuration, firewall rules, and distribution of the public CA certificate;
`hamsterd` does not provide client authentication.

### Develop against a real site without deploying

Suppose `https://app.example.com` serves its API below `/api/`, while a local
`npm run dev` server provides the frontend and `/assets/`. `lemmingd` can send
the frontend routes to localhost and leave `/api/` on the real server:

| Browser-visible request | Served by |
| --- | --- |
| `https://app.example.com/` | Local frontend |
| `https://app.example.com/assets/app.js` | Local frontend |
| `https://app.example.com/api/users` | Real remote API |

The browser keeps the real HTTPS scheme, hostname, cookies, redirects, and API
behavior. Frontend changes appear on refresh—without `npm build`, CI/CD, a
preview environment, or a deployment. Reverse the rule to test a local API
against the deployed frontend, or give local frontend and API servers separate
browser-visible hostnames to exercise real browser CORS checks and iterate on
the local API's CORS headers.

Use QA or staging whenever possible. If the remote routes point to production,
its data and side effects remain real.

### Preview a hosting migration before changing DNS

`lemmingd` can also route a site's real hostname to a candidate hosting origin.
Configure one browser or browser profile to use the proxy and leave another
browser direct. The proxied browser shows the candidate host while the direct
browser follows public DNS to the current host, so a developer can compare both
versions at the same URL in real time without editing `/etc/hosts` or changing
DNS. This complements one-off checks such as `curl --resolve` with a complete,
interactive browser session.

## User guides

- [AI/LLM project index](llms.txt) provides a compact map of the project and
  its authoritative documentation.
- [Use lemmingd](docs/lemmingd.md) to combine a local frontend or backend with
  selected routes from a real site, or preview an alternate hosting origin
  before changing DNS.
- [Use hamsterd](docs/hamsterd.md) to cache eligible public development
  downloads.
- [Install the local CA](docs/ca-installation.md) in Firefox,
  Chrome/Chromium-family browsers, Safari, Linux, macOS, or Windows.

## Requirements

- Go 1.26 or newer to build from source; release binaries are built with the
  Go version in the `go.mod` `toolchain` line
- A client that supports an HTTP proxy
- Firefox and Mozilla NSS `certutil` only when using `lemmingd firefox`
- `rpkg`, `rpmbuild`, and `mock` only when building RPMs

## Install

### Go

```sh
go install github.com/BOPOHA/hamsterd/cmd/hamsterd@latest
go install github.com/BOPOHA/hamsterd/cmd/lemmingd@latest
```

`go install` builds from source with your Go toolchain (Go 1.26 or newer) and
reports the module version, such as `hamsterd v0.2.0`.

### Release archives

Download Linux, macOS, or Windows archives (amd64 and arm64) from the
[latest release](https://github.com/BOPOHA/hamsterd/releases/latest), then
[verify them](#verify-a-release-download).

### Homebrew

Starting with v0.2.0:

```sh
brew install --cask bopoha/tap/hamsterd bopoha/tap/lemmingd
```

The casks are published from each release to
[`BOPOHA/homebrew-tap`](https://github.com/BOPOHA/homebrew-tap).

### Container image (hamsterd)

Starting with v0.2.0:

```sh
docker run -d -p 192.168.10.5:8080:8080 -v hamsterd-state:/var/lib/hamsterd \
  ghcr.io/bopoha/hamsterd:latest
```

The image is meant for a shared cache on a trusted network; see
[Run in a container](docs/hamsterd.md#run-in-a-container) and the
[Compose example](examples/docker-compose.yml). `lemmingd` routes to servers on
the developer's own machine, so it is not packaged as an image.

### Fedora, EPEL, and Amazon Linux (COPR)

Ready-made `hamsterd` and `lemmingd` RPM packages are available from the
unofficial [`vorona/hamsterd`](https://copr.fedorainfracloud.org/coprs/vorona/hamsterd/)
COPR repository:

```sh
sudo dnf copr enable vorona/hamsterd
sudo dnf install hamsterd lemmingd
```

The packages are currently built for:

| Release | Architectures |
| --- | --- |
| Amazon Linux 2023 | `aarch64`, `x86_64` |
| EPEL 10 | `aarch64`, `x86_64` |
| Fedora 44 | `aarch64`, `x86_64` |
| Fedora 45 | `aarch64`, `x86_64` |
| Fedora Rawhide | `aarch64`, `x86_64` |

These repositories are provided as-is by the project owner. Report package
issues to the project rather than to distribution Bugzilla instances.

## Build

```sh
make build
./bin/hamsterd -version
./bin/lemmingd -version
```

`make all` additionally creates Linux, macOS, and Windows amd64 release-style
binaries in `bin/`. Dependencies are standard Go modules; `vendor/` is neither
required nor stored in Git.

Version output identifies the kind of build without empty metadata fields:

- local `make build`: `hamsterd <git-describe>`;
- RPM: `hamsterd <semver>`;
- GitHub release: `hamsterd <semver>+<short-git-hash>`.

## hamsterd

`hamsterd` caches eligible public downloads for repeated builds, disposable
environments, and controlled shared development caches. Start it with:

```sh
./bin/hamsterd
```

The complete walkthrough, deterministic cache test, use cases, cache policy,
configuration, and troubleshooting are in
[Using hamsterd](docs/hamsterd.md). See also the
[example configuration](examples/hamsterd.json) and the
[Terragrunt caching walkthrough](docs/hamsterd.howto.md).

## lemmingd

`lemmingd` combines selected local frontend, backend, mock, asset, or alternate
hosting routes with a real remote application. Start it once to create its
configuration:

```sh
./bin/lemmingd
```

The complete local-frontend/remote-API walkthrough, hosting-migration preview,
CORS and HTTPS examples, routing semantics, dev-server checklist, and
troubleshooting are in
[Using lemmingd](docs/lemmingd.md). See also the
[multi-rule example configuration](examples/lemmingd.json), adapted and
anonymized from a real development setup.

To launch an isolated Firefox instance that is already configured to use
lemmingd and trust its CA, install `certutil` (`nss-tools` on Fedora/RHEL or
`libnss3-tools` on Debian/Ubuntu), then run:

```sh
./bin/lemmingd firefox https://app.example.com/
```

It creates or reuses a lemmingd-owned Firefox profile beside the lemmingd
configuration. Your usual Firefox profile remains direct and unchanged.
On Windows, pass `-nss-certutil PATH` for the NSS `certutil` executable; the
built-in Windows `certutil.exe` cannot configure Firefox trust.

## Configuration

Common options:

| JSON field | Meaning |
| --- | --- |
| `version` | Configuration format; currently `1`. |
| `listen` | Proxy address. Defaults to `127.0.0.1:8080` or `127.0.0.1:18080`. |
| `allow_remote_clients` | Required before a non-loopback listen address is accepted. It refuses loopback, link-local, and metadata destinations and limits `CONNECT` ports, but does not provide authentication. |
| `allowed_connect_ports` | With `allow_remote_clients`: `CONNECT` ports that clients may use. Defaults to `[443]`. |
| `domains` | `hamsterd` only: exact hostnames to intercept and cache; an empty or omitted list means all hosts. |
| `allow_remote_targets` | `lemmingd` only: permits routing to non-loopback targets. Remote plain-HTTP targets must be written as `http://host:port`. |

Command-line options override paths or the listen address. The Firefox launcher
is `lemmingd firefox [options] URL`; its two Firefox-specific options are
marked below:

```text
-config PATH
-cacert PATH
-cakey PATH
-listen HOST:PORT
-firefox-bin PATH        # lemmingd firefox only
-nss-certutil PATH       # lemmingd firefox only
-version
```

The 2022 `Socket`, `CustomConfig`, `LocalSocket`, `Domains`, `StartWithWL`, and
`StartWithBL` JSON names are still understood when an old config is supplied
with `-config`. Old wildcard listen addresses such as `:8080` are converted to
loopback unless remote clients are explicitly enabled.

## CA security

HTTPS interception works only after the client trusts the generated CA. Trust
it only in the development client or container that uses the proxy. Never copy
or publish `ca.key`, and do not expose either proxy to an untrusted network.

A generated CA is valid for two years and cannot issue intermediate CAs. Each
intercepted host receives a short-lived certificate with its own key. Thirty
days before the CA expires, the proxy logs a warning; to rotate it, remove the
old CA from client trust stores, delete `ca.crt` and `ca.key`, and restart.
CAs created by earlier releases remain valid until their own expiry date.

The CAs distributed by historical releases had public private keys and are
unsafe. The new binaries refuse all known historical certificates. Remove them
from every trust store and delete old `~/.hamsterd/ca.crt` and
`~/.hamsterd/ca.key` files.

See [SECURITY.md](SECURITY.md) for the security model and reporting guidance.

## Verify a release download

Release archives have SHA-256 checksums, SPDX SBOMs, and GitHub build
provenance attestations; the container image has a provenance attestation and
an SBOM:

```sh
sha256sum --ignore-missing -c checksums.txt
gh attestation verify hamsterd_0.2.0_linux_amd64.tar.gz --repo BOPOHA/hamsterd
gh attestation verify oci://ghcr.io/bopoha/hamsterd:v0.2.0 --repo BOPOHA/hamsterd
```

## Development

```sh
make deps       # download modules
make test       # unit and integration tests
make vet        # go vet
make build      # current-platform binaries
make all        # release-style cross-builds
```

The race-enabled test suite uses only local temporary servers; it does not
require external HTTP services.

## RPM and COPR builds

The RPM source build uses the same approach as `go-openlawsvpn`: an `rpkg`
macro generates a temporary vendor tarball while creating the SRPM. The Git
repository remains vendor-free, and `mock` performs the binary build without
network access.

```sh
make srpm
make rpm
make fc
```

One source spec produces independently installable `hamsterd` and `lemmingd`
RPM packages. Build artifacts are written below `rpmbuild/` and `rpm-results/`.

`make clean` removes `bin/`, `dist/`, `rpmbuild/`, `rpm-results/`, a temporary
`vendor/` tree, and root-level Go test and coverage artifacts.

To create the COPR package, select the SCM source type and use:

| COPR field | Value |
| --- | --- |
| Clone URL | `https://github.com/BOPOHA/hamsterd.git` |
| Committish | `main` (or a release tag) |
| Subdirectory | leave empty |
| Spec file | `packaging/hamsterd.spec` |
| Build SRPM with | `make srpm` |
| Build dependencies | `golang` |

Enable the desired Fedora chroots and trigger a build. The SCM source builder
uses `.copr/Makefile` to invoke the repository's `rpkg.conf` and
`packaging/rpkg.macros`, download Go modules, and attach
`hamsterd-go-vendor.tar.gz` to the SRPM. The architecture builders then compile
and test with `-mod=vendor`, without network access.

After the first successful build, enable COPR auto-rebuild and add the webhook
shown under the project's **Settings > Integrations** page to the GitHub
repository if builds should follow pushes automatically.

## License

The project is MIT licensed. Statically linked dependencies retain their
respective license notices in the generated source dependency archive.
