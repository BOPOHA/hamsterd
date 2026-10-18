# TODO

Here are my own TODO things that are on the table or in my mind. Released
work lives in [CHANGELOG.md](CHANGELOG.md); this file lists only what is still
open, roughly in the order I plan to take it.

`lemmingd` stays a developer-workstation tool that routes a real site to that
developer's local servers. `hamsterd` also serves as shared team
infrastructure: an office or lab cache for `dnf`/`apt` updates, toolchains, and
CI/CD artifacts.

## On the table

- [ ] Publish the Homebrew casks to `BOPOHA/homebrew-tap` through the GitHub
      bot: mint a short-lived token in the release workflow and pass it as
      `HOMEBREW_TAP_TOKEN`. The README already documents `brew install`.
- [ ] Check that the COPR builds for EPEL 10 and Amazon Linux 2023 succeed with
      their system Go (0.2.0 requires `golang >= 1.26`).
- [ ] JSON Schema files for both configurations (`schema/*.schema.json`),
      referenced by `$schema` in the examples, for validation in editors and by
      coding agents.
- [ ] `llms-full.txt` generated in CI from all documentation, alongside the
      existing `llms.txt` index, plus task-oriented recipes for agents.
- [ ] `AGENTS.md` for contributors' coding agents: build, test, and lint
      commands, invariants, and files that must never be committed (`ca.key`).
- [ ] Tested hamsterd recipes: `dnf`/`yum`, `apt`, Playwright/Puppeteer
      browsers, Node.js/npm, pip, Go modules, cargo, JDK and SDK tarballs,
      Docker `build --build-arg HTTPS_PROXY` with CA injection, GitHub Actions
      self-hosted runners, and GitLab CI `services:`. Each recipe lists the
      proxy variables and CA settings (`NODE_EXTRA_CA_CERTS`,
      `REQUESTS_CA_BUNDLE`, `SSL_CERT_FILE`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO`).
- [ ] lemmingd framework recipes: Vite, Next.js, webpack-dev-server, Angular CLI
      (HMR over `wss://`, `allowedHosts`, `server.hmr.clientPort`).

## Next

- [ ] hamsterd client access control: a source-CIDR allowlist and optional
      `Proxy-Authorization` (Basic) credentials, so a shared cache does not
      rely only on firewall rules.
- [ ] hamsterd revalidation with `If-None-Match` / `If-Modified-Since` instead
      of refetching expired entries. Needed for package-manager metadata such
      as `repomd.xml` and `InRelease`, which change in place.
- [ ] hamsterd per-host or per-path TTL rules (long for content-addressed
      package files, short for repository metadata).
- [ ] lemmingd remote targets per rule (`"allow_remote": true` on a rule)
      instead of only the global `allow_remote_targets`.
- [ ] hamsterd metrics and status: `/status.json` (hits, misses, bypasses,
      bytes saved, cache size, intercepted domains) and an optional Prometheus
      endpoint.
- [ ] Machine-readable output: a `-log-format json` option and documented exit
      codes.

## Later

### Distribution

- [ ] Debian and Ubuntu packages (`.deb`) built by goreleaser `nfpms`, attached
      to GitHub releases; later an APT repository or a Launchpad PPA.
- [ ] Windows: Scoop bucket and a winget manifest.
- [ ] Arch Linux AUR packages (`hamsterd-bin`, `lemmingd-bin`).
- [ ] Nix flake / nixpkgs package.
- [ ] Optional companion RPM such as `hamsterd-daemon` for system-service
      operation (systemd unit, dedicated user, hardened unit options) and
      explicit system CA trust lifecycle management. Keep it separate from the
      unprivileged development package and never install a trusted CA
      implicitly. Equivalent units for `.deb` and a launchd plist for macOS.
- [ ] Cosign keyless signatures for archives and container images, in addition
      to GitHub attestations.
- [ ] Submit the Homebrew casks to homebrew-core once the project has enough
      users.

### hamsterd

- [ ] Cache administration commands: list, purge by URL or host, show size.
- [ ] Optional refusal of private-network (RFC 1918, ULA) destinations for
      deployments that do not need internal mirrors.
- [ ] A configurable connection limit and remote-mode idle timeout.

### lemmingd

- [ ] Wildcard domains (`*.example.com`) with explicit opt-in.
- [ ] Optional response header rewriting for local development (for example,
      relaxing a CSP or HSTS header for the routed paths only).
- [ ] `lemmingd chrome URL`: an isolated Chromium-family profile launcher, like
      `lemmingd firefox`.

### Certificates and protocols

- [ ] Optional name-constrained CA (`PermittedDNSDomains`) generated from the
      configured `domains` (hamsterd) or rule domains (lemmingd), regenerated
      when that list changes, so a leaked key cannot impersonate other sites.
- [ ] `hamsterd ca` / `lemmingd ca` subcommands: print path, fingerprint, and
      expiry; rotate the CA.
- [ ] Offer HTTP/2 to intercepted clients after the HTTP/2 server path has the
      same header, stream, and timeout limits as the HTTP/1.1 path.

### AI agents

- [ ] An MCP server or Claude Code skill to start a proxy with a given rule set,
      report cache statistics, and launch the isolated browser profile.

### Project and community

- [ ] "Why not X?" comparison with mitmproxy, Charles/Proxyman, Requestly,
      Squid, Artifactory/Nexus/Athens, and `/etc/hosts` editing.
- [ ] A security-model diagram: where the CA lives, what is intercepted, what
      is cached.
- [ ] `CONTRIBUTING.md`, issue and pull-request templates, `CODE_OF_CONDUCT.md`,
      and `good first issue` labels.
- [ ] OpenSSF Scorecard workflow, with its badge in the README, and the OpenSSF
      Best Practices badge.
- [ ] Launch posts (Show HN, r/devops, r/golang, r/Terraform, r/webdev, a blog
      post) and submissions to awesome-go and awesome-devops lists.
