# Changelog

All notable changes will be documented in this file. Releases follow semantic
versioning while the project remains below version 1.0.

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
