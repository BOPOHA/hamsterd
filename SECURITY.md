# Security policy

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

Authenticated, cookie-bearing, ranged, private, and otherwise ambiguous
responses are excluded from `hamsterd`'s cache. Cache and key files are private
to the operating-system user by default.

## Shared development cache

`hamsterd` can serve a trusted development LAN, lab, or build farm, but
`allow_remote_clients` is only an explicit safety acknowledgement: it does not
enable authentication or authorization. Bind to a private interface, restrict
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
