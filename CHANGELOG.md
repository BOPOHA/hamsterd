# Changelog

All notable changes will be documented in this file. Releases follow semantic
versioning while the project remains below version 1.0.

## Unreleased

- Replace the unmaintained proxy and cache forks.
- Generate a unique local CA instead of distributing a shared private key.
- Bind both proxies to loopback by default.
- Separate common proxy infrastructure from caching and redirect behavior.
- Add bounded, private, security-conscious disk caching.
- Add automated tests, binary releases, and source RPM packaging.
