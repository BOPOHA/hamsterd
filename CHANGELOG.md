# Changelog

All notable changes will be documented in this file. Releases follow semantic
versioning while the project remains below version 1.0.

## [0.2.0] - 2026-10-18

### Security

- Update `golang.org/x/net` to v0.61.0, fixing reachable HTTP/2
  denial-of-service vulnerabilities (GO-2026-6603, GO-2026-6611,
  GO-2026-6612, GO-2026-6617), and `github.com/elazarl/goproxy` to v1.9.2.
  Release builds use the Go toolchain named in `go.mod` (1.27.2); building from
  source requires Go 1.26 or newer.
- With `allow_remote_clients`, refuse destinations on the proxy host's
  loopback interface, link-local and cloud metadata addresses, and the proxy's
  own address, checked after DNS resolution. lemmingd's configured targets
  remain reachable.
- With `allow_remote_clients`, allow `CONNECT` only to port 443 or the ports in
  the new `allowed_connect_ports` setting, and close tunnels idle for 15
  minutes.
- Cap concurrent client connections, and apply the outer listener's header
  size and timeout limits to requests inside intercepted HTTPS tunnels. Reject
  intercepted requests whose `Host` differs from the `CONNECT` host, and
  nested `CONNECT` requests.
- Return generic `502` and `403` responses instead of upstream error text.
- Drop request headers listed in `Connection`.
- Generate new CAs with an explicit zero path-length constraint and a two-year
  lifetime, and warn 30 days before a CA expires.
- Issue intercepted-host certificates valid for 30 days, each with a random key
  and 128-bit serial number, renewed before expiry and generated concurrently
  per host.
- Refuse configuration files writable by other users.
- hamsterd: bypass the cache for credential-like request headers and for
  link-local and cloud metadata hosts; apply `default_ttl` only to
  responses with an `ETag` or `Last-Modified` validator.
- hamsterd: count stored headers toward `max_size_mib`, limit stored headers
  to 64 KiB and upstream response headers to 256 KiB, limit temporary data
  for downloads in progress, remove leftover temporary files at startup, and
  install a body and its metadata atomically with respect to readers.
- hamsterd: refuse a cache directory that contains files it did not create,
  so eviction and permission changes affect only its own files.
- lemmingd: accept `http://host:port` and `https://host:port` targets, and
  require the explicit `http://` form for remote plain-HTTP targets.
- Build the RPM vendor archive with a private, verified module cache, and fail
  when the archive cannot be written.
- Gate releases on the full CI suite; pin every action by commit; add
  Dependabot and SPDX SBOMs covered by provenance attestations; test the
  minimum Go version.

### Changed

- hamsterd caches an index in memory instead of scanning the cache directory
  after every stored response.
- Browser disconnects during TLS handshakes are no longer logged.

## [0.1.4] - 2026-10-09

- Run golangci-lint in CI and document locking and quoting invariants.
- Wait for Firefox cleanup in tests.

## [0.1.3] - 2026-10-07

- Add `lemmingd firefox URL` to launch an isolated Firefox profile configured
  with lemmingd's proxy and public CA.
- Keep Firefox's default profile and profile registry unchanged, and require
  NSS `certutil` rather than weakening HTTPS verification.
- Recommend `nss-tools` in the lemmingd RPM for its optional Firefox launcher.
- Track Firefox exits to remove its launcher lease, serialize managed-profile
  preparation, safely replace stale runtime leases, reject active Firefox
  profile locks, stop Firefox when post-launch setup fails, and include NSS
  command diagnostics when setup fails.

## [0.1.2] - 2026-10-06

- Preserve the browser-visible `Host` header and ordinary request headers when
  lemmingd routes a request to a configured target.
- Support HTTPS lemmingd targets on port `443`, with normal target-hostname TLS
  verification.
- Document side-by-side hosting-migration previews before a DNS cutover and add
  a ready-to-edit remote-target example configuration.

## [0.1.1] - 2026-10-05

- Replace the unmaintained proxy and cache forks.
- Generate a unique local CA instead of distributing a shared private key.
- Bind both proxies to loopback by default.
- Separate common proxy infrastructure from caching and redirect behavior.
- Add bounded, private, security-conscious disk caching.
- Allow hamsterd to limit interception and caching to configured domains.
- Cache safe `Accept-Encoding` and `Origin` response variants independently.
- Serve `stale-while-revalidate` entries immediately and refresh them in the
  background.
- Include the request hostname in intercepted-response write warnings.
- Add automated tests, binary releases, and source RPM packaging.
