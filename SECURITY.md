# Security policy

## Supported versions

Security fixes are released for the latest minor version only. Before version
1.0, upgrade to the newest `0.x` release to receive fixes.

| Version | Supported |
| --- | --- |
| 0.2.x | Yes |
| < 0.2 | No |

`hamsterd` and `lemmingd` are TLS interception tools. A trusted proxy CA can
issue certificates for any hostname, so compromise of its private key has the
same practical effect as compromise of any locally trusted certificate
authority.

## Safe operation

- Keep the default loopback listen address.
- Trust the generated CA only in dedicated development clients or containers.
- Never share or commit a generated `ca.key`.
- Do not use these proxies for banking, personal accounts, production secrets,
  or other sensitive traffic.
- Do not enable remote clients without separate network access controls.
- Keep upstream TLS verification enabled.

The optional `lemmingd firefox URL` command installs only the public lemmingd
CA into a dedicated Firefox profile; it does not modify the default Firefox
profile or an operating-system trust store. That managed profile can retain
cookies, history, sessions, saved logins, and other authenticated browsing
state. Close it when testing is complete, protect it as sensitive development
data, and remove the dedicated profile when it is no longer needed.

Authenticated, cookie-bearing, token-bearing, ranged, private, and otherwise
ambiguous responses are excluded from `hamsterd`'s cache. Cache and key files
are private to the operating-system user by default, and configuration files
writable by other users are refused.

## Certificate authority scope

A generated CA is valid for two years and has a zero path-length constraint,
so its key cannot create intermediate CAs. Intercepted hosts receive
certificates valid for 30 days, each with its own random key and serial
number; these leaf keys exist only in memory.

## lemmingd control API

The optional control API used by `lemmingd mcp` is disabled unless
`config.json` sets `"control_api": true`. It listens only on a random
`127.0.0.1` port, separate from the proxy listener, and requires a random
256-bit token that `lemmingd` writes at each start to `control.json` in its
private configuration directory, mode `0600`. Requests addressed by any other
`Host`, or carrying browser `Origin` or `Sec-Fetch-*` headers, are refused to
prevent DNS rebinding and cross-site requests. A local process that can read
the configuration directory can therefore control `lemmingd`; it could equally
read `config.json` and `ca.key`.

Rules added through the API are written to `rules.auto.json`, which accepts
only routing rules. Agents cannot enable remote clients or remote targets,
change the listen address, remove rules from `config.json`, or read `ca.key`.

## Shared development cache

`hamsterd` can serve a trusted development LAN, lab, or build farm, but
`allow_remote_clients` does not enable authentication or authorization. It does
enable defense in depth: destinations on the proxy host's loopback interface,
link-local and cloud metadata addresses, and the proxy's own address are
refused after DNS resolution; `CONNECT` is limited to port `443` unless
`allowed_connect_ports` lists others; connections are capped; and idle tunnels
are closed. Bind to a private interface, restrict
source addresses with host or network firewall rules, and use an exact
`domains` allowlist whenever practical. Never expose the proxy to the internet
or an untrusted network.

Authorized clients need the public `ca.crt` to use intercepted HTTPS downloads.
The server's `ca.key` must remain only on the proxy host. If that key is copied
or compromised, remove the CA from every client trust store and generate a new
one before using the proxy again.

## Historical CA warning

Releases derived from the old `master` and `dev` branches included three public
CA private keys: the early hamsterd CA, the early lemmingd CA, and the bundled
go-httpproxy CA. Remove all historical hamsterd and lemmingd CAs from trust
stores. New versions recognize and reject all three certificates rather than
silently reusing them.

Their SHA-256 certificate fingerprints are:

```text
69:18:35:60:2A:47:04:BA:77:ED:31:FA:39:0C:B3:95:64:D8:BB:DE:10:29:49:2C:EE:C8:AE:6D:B5:24:22:07
35:AA:FC:DB:D8:3F:64:26:46:78:3D:49:C3:63:46:90:37:43:EC:17:43:5A:5F:4C:37:85:F0:2F:C1:07:F8:1F
AF:73:9A:82:99:C3:61:17:B9:10:5C:28:41:F4:B7:F3:64:36:7D:3B:B3:AB:DA:72:15:FB:9E:EC:18:84:1D:6F
```

## Reporting a vulnerability

Please use GitHub's private security-advisory reporting mechanism for this
repository. Do not include private keys, credentials, captured traffic, or
other user data in a public issue.
