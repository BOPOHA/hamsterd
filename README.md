# hamsterd and lemmingd

This repository contains two small local HTTP/HTTPS development proxies. They
share secure proxy, configuration, and certificate-authority infrastructure,
but they have different jobs:

| Command | Purpose | HTTPS behavior |
| --- | --- | --- |
| `hamsterd` | Cache public downloads locally to speed up repeated builds. | Intercepts all HTTPS requests so it can cache eligible responses. |
| `lemmingd` | Route selected domains and URL paths to local development servers. | Intercepts configured domains only; other CONNECT traffic is tunneled unchanged. |

Both commands listen on loopback by default. They generate a unique local CA on
first start and never install it into a trust store automatically.

## Requirements

- Go 1.25 or newer
- A client that supports an HTTP proxy
- `rpkg`, `rpmbuild`, and `mock` only when building RPMs

## Install from COPR

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

Start the caching proxy:

```sh
./bin/hamsterd
```

On first start it creates:

```text
${XDG_CONFIG_HOME:-$HOME/.config}/hamsterd/config.json
${XDG_CONFIG_HOME:-$HOME/.config}/hamsterd/ca.crt
${XDG_CONFIG_HOME:-$HOME/.config}/hamsterd/ca.key
${XDG_CACHE_HOME:-$HOME/.cache}/hamsterd/
```

Test the health endpoint and download the public CA certificate:

```sh
curl http://127.0.0.1:8080/healthz
curl -o hamsterd-ca.crt http://127.0.0.1:8080/ca.crt
```

Test an HTTPS request without changing the system trust store:

```sh
curl --proxy http://127.0.0.1:8080 \
  --cacert "${XDG_CONFIG_HOME:-$HOME/.config}/hamsterd/ca.crt" \
  -D - https://example.com/ -o /dev/null
```

Repeat the request and inspect `X-Hamsterd-Cache`: the first eligible response
is `MISS`, and the next is `HIT`.

### Cache policy

The cache deliberately favors safety over maximum hit rate. It stores only
complete `GET` responses with status 200. It bypasses requests containing
authorization, cookies, ranges, `no-cache`, or `no-store`, and responses with
`Set-Cookie`, `Vary`, `Content-Range`, `private`, `no-cache`, or `no-store`.

Objects are written atomically with mode `0600`. The cache directory is `0700`.
Object and total-cache limits are configurable. Eviction removes the
least-recently-used files when the configured total size is exceeded.

Example configuration: [examples/hamsterd.json](examples/hamsterd.json).

## lemmingd

`lemmingd` lets a browser or command-line client request a real hostname while
selected paths are served by a local HTTP process. This is useful when testing
local static assets or frontends against an otherwise remote application.

Run it once to create its configuration, stop it, and add routing rules:

```sh
./bin/lemmingd
${EDITOR:-vi} "${XDG_CONFIG_HOME:-$HOME/.config}/lemmingd/config.json"
./bin/lemmingd
```

For each rule:

1. `exclude_paths` is checked first.
2. A matching `include_paths` prefix is rewritten to `http://target`.
3. A nonmatching path continues to the original HTTPS server.
4. Domains not present in any rule use a transparent CONNECT tunnel and are not
   intercepted.

Example configuration: [examples/lemmingd.json](examples/lemmingd.json).

With that example and a local server on port 8000:

```sh
python3 -m http.server 8000
curl --proxy http://127.0.0.1:18080 \
  --cacert "${XDG_CONFIG_HOME:-$HOME/.config}/lemmingd/ca.crt" \
  https://static.example.com/static/example.txt
```

## Configuration

Common options:

| JSON field | Meaning |
| --- | --- |
| `version` | Configuration format; currently `1`. |
| `listen` | Proxy address. Defaults to `127.0.0.1:8080` or `127.0.0.1:18080`. |
| `allow_remote_clients` | Required before a non-loopback listen address is accepted. It does not provide authentication by itself. |
| `allow_remote_targets` | `lemmingd` only: permits routing to non-loopback targets. |

Command-line options override paths or the listen address:

```text
-config PATH
-cacert PATH
-cakey PATH
-listen HOST:PORT
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

The CAs distributed by historical releases had public private keys and are
unsafe. The new binaries refuse all known historical certificates. Remove them
from every trust store and delete old `~/.hamsterd/ca.crt` and
`~/.hamsterd/ca.key` files.

See [SECURITY.md](SECURITY.md) for the security model and reporting guidance.

## Development

```sh
make deps       # download modules
make test       # unit and integration tests
make vet        # go vet
make build      # current-platform binaries
make all        # release-style cross-builds
```

The test suite uses only local temporary servers; it does not require external
HTTP services.

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
