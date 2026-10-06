# Using lemmingd

**Route part of a real site to a development server or alternate host under
its real HTTPS URL, without deploying it or changing DNS.**

`lemmingd` lets a browser keep using a real HTTPS URL while selected requests
are served by a development server on your machine or an alternate remote
origin. The browser still sees `https://app.example.com`; `lemmingd` decides
which requests go to the current site and which go to the configured target.

This is useful when a frontend engineer needs real QA or production-like APIs,
cookies, redirects, and HTTPS without running the complete backend locally. A
change can go from editor to browser refresh without `npm build`, CI/CD, a
preview environment, or a deployment.

> **Use QA or another non-production environment whenever possible.** A local
> frontend can still make real writes through the remote API. It can also send
> cookies and other sensitive request data through the local proxy.

## The basic workflow

1. Start `lemmingd` once. It creates its configuration and unique CA:

   ```sh
   lemmingd
   ```

2. Stop it with `Ctrl+C`, then edit:

   ```text
   ~/.config/lemmingd/config.json
   ```

   If `XDG_CONFIG_HOME` is set, the file is under
   `$XDG_CONFIG_HOME/lemmingd/` instead. These are Linux paths; on other
   platforms, use the path printed in the startup log.

3. Start the local frontend, for example:

   ```sh
   npm run dev
   ```

4. Start `lemmingd` again. Configuration changes are read at startup, so
   restart it after every change:

   ```sh
   lemmingd
   ```

5. Configure the browser to use `127.0.0.1:18080` as both its HTTP and HTTPS
   proxy, and trust the generated `ca.crt` in that development browser. See
   [Installing the local CA](ca-installation.md).

6. Open the real URL, such as `https://example.com`. Do not open the local dev
   server's port directly.

Opening `http://127.0.0.1:18080/` directly only shows the proxy status page. It
is not a configuration interface.

## Launch an isolated Firefox profile

For a side-by-side direct-versus-proxied browser session, lemmingd can prepare
and launch its own Firefox profile:

```sh
lemmingd firefox https://www.foo-bar-example.test/
```

The command starts lemmingd, creates or reuses `firefox-profile` beside its
`config.json`, imports lemmingd's public `ca.crt` into that profile, configures
its HTTP and HTTPS proxy with lemmingd's effective listen address, and launches
Firefox with that profile. The default is `127.0.0.1:18080`; a wildcard listen
address, including `:18080`, is converted to a loopback address for the local
browser. The command does not modify Firefox's default profile or Firefox's
`profiles.ini`. A normal Firefox instance can therefore stay direct while the
lemmingd-managed instance shows a configured local or migration target.

Install `certutil` first: `nss-tools` on Fedora/RHEL or `libnss3-tools` on
Debian/Ubuntu. The command fails rather than disabling TLS verification when
that prerequisite is unavailable. The RPM recommends `nss-tools`; install it
explicitly if weak dependencies are disabled. Lemmingd refuses to modify a
managed profile while either Firefox itself or another lemmingd launcher holds
it open. To use a non-standard Firefox executable, pass `-firefox-bin` before
the URL:

```sh
lemmingd firefox -firefox-bin /path/to/firefox https://www.foo-bar-example.test/
```

The managed profile normally lives at `~/.config/lemmingd/firefox-profile/` on
Linux, `~/Library/Application Support/lemmingd/firefox-profile/` on macOS, and
`%AppData%\lemmingd\firefox-profile\` on Windows. Firefox is found
automatically from `PATH`, from `/Applications/Firefox.app` or
`~/Applications/Firefox.app` on macOS, and from the usual Program Files or
per-user installation locations on Windows. Pass `-firefox-bin PATH` when it
is installed elsewhere. On Windows, pass
`-nss-certutil PATH` for the NSS `certutil` executable: Windows' built-in
`certutil.exe` is a different program and cannot configure Firefox trust.

On macOS, install NSS with your package manager, for example `brew install
nss`. Homebrew normally places the executable at
`/opt/homebrew/opt/nss/bin/certutil` on Apple Silicon or
`/usr/local/opt/nss/bin/certutil` on Intel Macs; make it available on `PATH` or
pass that absolute path with `-nss-certutil`. On Windows, automated CA setup
requires a separately supplied Mozilla NSS `certutil.exe`; pass it through
`-nss-certutil` and do not substitute the Windows system utility. Lemmingd does
not bundle an NSS distribution for Windows. It intentionally does not add its
CA to the macOS Keychain or Windows certificate store, because that would trust
the CA outside the isolated Firefox profile.

The managed profile retains its own cookies, history, sessions, saved logins,
and other Firefox data. Treat it as sensitive development data. Closing Firefox
does not stop lemmingd. Lemmingd does not explicitly close Firefox when it
stops, though terminal-delivered signals can also reach Firefox; close both when
finished. A Firefox window left open after lemmingd stops remains configured
for the now-unavailable proxy. Start normal
`lemmingd` again and reload that existing window, or close Firefox and stop any
existing lemmingd process before using `lemmingd firefox URL` again. The
combined command starts a new proxy; it cannot attach to one already listening.

To remove the profile, first close its Firefox window and lemmingd, then delete
only the `firefox-profile` directory shown in the startup log. This removes its
CA trust and all browsing data stored in that managed profile; it does not
affect the normal Firefox profile. A stale launcher lease left by a crash is
replaced automatically once Firefox's native profile lock is gone. If Firefox
itself crashed and left one of its profile-lock files behind, first confirm that
no Firefox process still uses this profile before removing that lock file and
retrying. A lemmingd crash during profile preparation can instead leave its
`.lemmingd-firefox.prepare` directory; after confirming that no other lemmingd
process is preparing this profile, remove that directory and retry.

## Main use case: local frontend, real API, no deployment

Suppose the real application is `https://example.com`, the local frontend runs
on port `5173`, and the real API is below `/api/`.

```json
{
  "version": 1,
  "listen": "127.0.0.1:18080",
  "rules": [
    {
      "target": "127.0.0.1:5173",
      "domains": ["example.com"],
      "include_paths": ["/"],
      "exclude_paths": ["/api/"]
    }
  ]
}
```

With this rule:

| Browser requests | Actual destination |
| --- | --- |
| `https://example.com/` | Local frontend on port `5173` |
| `https://example.com/src/app.js` | Local frontend on port `5173` |
| `https://example.com/api/users` | Real `example.com` server |

Run `npm run dev`, open `https://example.com`, and edit locally. The browser
loads the new frontend immediately while `/api/` keeps using the real remote
stack and data. This makes it quick to reproduce a frontend defect, develop a
fix, or demonstrate a change in its real integration context before creating a
build or starting a deployment pipeline.

The path and query string are preserved. Exclusions are checked before
inclusions, which is why the broad `/` rule does not capture `/api/`.

Path matching is literal prefix matching. `/api/` does not match the exact path
`/api`; add both if the application uses both forms.

The [complete example configuration](../examples/lemmingd.json) is adapted
from a real development setup and demonstrates more of the available options:

- multiple production-like and staging hostnames sharing one local target;
- replacing only `/static/` while keeping `/static/generated/` remote;
- replacing an entire frontend while keeping `/api/` and `/env.js` remote;
- separate local targets for an asset server and a frontend dev server; and
- explicit remote-client and remote-target safety settings.

All example hostnames use reserved `example.com` or `.test` names. Replace them
with domains you are authorized to test.

For a copy-paste walkthrough that replaces the theme of a real static site
while leaving its HTML remote, see
[Replace a static site's theme locally](lemmingd.howto.md).

### Dev-server checklist

Modern frontend dev servers often need a little more than the first page:

- Use relative asset and API URLs where possible. A URL hard-coded as
  `http://localhost:5173` bypasses `lemmingd` and may trigger mixed-content or
  CORS errors.
- If the application loads assets from another hostname, add a rule for that
  hostname too. A rule for `app.qa.example.com` does not cover
  `static.qa.example.com`.
- Configure hot-module replacement to use the browser-visible hostname and
  secure WebSockets, for example `wss://app.qa.example.com`. Its WebSocket path
  must also match an included path.
- The local server receives the browser-visible hostname in `Host`, while the
  connection itself goes to the configured target. Browser headers such as
  `Origin` and `Referer` can likewise contain the real HTTPS hostname. Account
  for that in dev-server host and origin checks.

If the page loads but hot reload or an asset does not, inspect the browser's
Network and Console panels and compare the failing hostname and path with the
configured rules.

## Testing the route before using a browser

For a configured HTTPS domain, test through the proxy with:

```sh
curl --proxy http://127.0.0.1:18080 \
  --cacert "${XDG_CONFIG_HOME:-$HOME/.config}/lemmingd/ca.crt" \
  -I https://example.com/
```

The `lemmingd` terminal logs every request that it routes locally:

```text
route host=example.com path=/ target=127.0.0.1:5173
```

If no route line appears, check the browser's proxy settings, domain, and path
prefixes.

## Strong use cases

### 1. Develop or reproduce a frontend issue without deploying

Use the configuration above: include `/` and exclude `/api/`. The HTML,
JavaScript, CSS, and other frontend routes come from `npm run dev`, while API
requests continue to QA, staging, or production.

This is useful for reproducing frontend-only bugs with realistic remote data,
validating a fix immediately, and demonstrating it before CI/CD finishes.
Prefer a test account and a non-production environment; the remote API remains
fully live and can still perform writes.

### 2. Real frontend with a local API implementation

Reverse the selection so only API paths go to a local backend:

```json
{
  "version": 1,
  "listen": "127.0.0.1:18080",
  "rules": [
    {
      "target": "127.0.0.1:3000",
      "domains": ["app.qa.example.com"],
      "include_paths": ["/api/"],
      "exclude_paths": []
    }
  ]
}
```

The deployed UI stays real, but calls below `/api/` reach the backend being
developed locally. This is handy for checking a response-shape change or a new
endpoint against the exact frontend version already deployed.

### 3. Preview a hosting migration before changing DNS

Route the public site hostname to the candidate hosting origin:

```json
{
  "version": 1,
  "listen": "127.0.0.1:18080",
  "allow_remote_targets": true,
  "rules": [
    {
      "target": "new-origin.example.net:443",
      "domains": ["www.example.com"],
      "include_paths": ["/"],
      "exclude_paths": []
    }
  ]
}
```

See [the ready-to-edit migration example](../examples/lemmingd-hosting-migration.json).

Configure one browser or browser profile to use lemmingd and leave a second
browser direct:

| Client | Opens | Content comes from |
| --- | --- | --- |
| Proxied browser | `https://www.example.com/` | Candidate host at `new-origin.example.net:443` |
| Direct browser | `https://www.example.com/` | Current host selected by public DNS |

Both browsers use the same public URL, so links, cookie behavior, JavaScript,
responsive layouts, and multi-page navigation can be compared in real time. No
DNS update or `/etc/hosts` change is needed, and only the browser configured to
use lemmingd is affected. This complements `curl --resolve`, which is excellent
for individual HTTP checks but does not provide a persistent interactive
browser session.

For an HTTPS target, use a target hostname whose certificate is valid for that
hostname; lemmingd uses it for TLS SNI and certificate verification. It sends
the original browser-visible hostname, `www.example.com` in this example, as
the HTTP `Host` header so the candidate server can select the site being
migrated. The candidate hosting configuration must accept that `Host` value.

Test logins, forms, redirects, asset hostnames, WebSockets, and service workers
explicitly. An absolute redirect to the target's internal hostname can leave
the public URL, and assets on additional domains need their own rules. Requests
carry real cookies and authorization data, and writes reach the selected
backend, so use test accounts and a non-production candidate when possible.

### 4. Develop and test CORS locally under realistic HTTPS origins

`localhost` ports alone do not reproduce the hostnames, HTTPS scheme, or
credentials mode used after deployment. Give the local frontend and API their
real browser-visible hostnames while routing them to separate local servers:

```json
{
  "version": 1,
  "listen": "127.0.0.1:18080",
  "rules": [
    {
      "target": "127.0.0.1:5173",
      "domains": ["app.qa.example.com"],
      "include_paths": ["/"],
      "exclude_paths": []
    },
    {
      "target": "127.0.0.1:3000",
      "domains": ["api.qa.example.com"],
      "include_paths": ["/"],
      "exclude_paths": []
    }
  ]
}
```

Configure the frontend to call `https://api.qa.example.com`. The browser sees
two distinct HTTPS origins and applies normal CORS rules, while both servers
remain local. Edit the local API's `Access-Control-Allow-*` headers and retest
on refresh. To test the local frontend against the real remote API instead,
omit the `api.qa.example.com` rule.

If both frontend and API use `https://example.com` with paths such as `/` and
`/api/`, they are the same origin. That setup is useful, but it does **not**
test CORS.

### 5. Replace only selected static assets

Keep the complete remote application but serve a CSS, JavaScript, image, or
localization subtree locally:

```json
{
  "version": 1,
  "listen": "127.0.0.1:18080",
  "rules": [
    {
      "target": "127.0.0.1:8000",
      "domains": ["static.example.com"],
      "include_paths": ["/static/"],
      "exclude_paths": ["/static/generated/"]
    }
  ]
}
```

This is a small, low-disruption way to verify a CSS, JavaScript, image, font,
translation, or theme fix against the real page. It is also useful when the
normal asset pipeline requires a full build and deployment for each change.

### 6. Reproduce HTTPS-only browser behavior

Serve the local application through a deployment-like HTTPS origin to inspect
Secure cookies, SameSite behavior, service workers, OAuth redirects, or code
that behaves differently in a secure context. The browser connects to
`https://app.qa.example.com`; `lemmingd` terminates that TLS connection and
forwards selected paths to the local HTTP server.

This reproduces the browser-visible scheme and hostname, but it does not
reproduce every property of the deployed edge, CDN, or production TLS setup.

### 7. Route selected calls to a local mock or failure simulator

Point an API path at a local mock server while leaving the UI and every other
route remote. The mock can return rare error codes, slow responses, empty
results, expired data, or malformed payloads that are difficult or unsafe to
create in a shared environment. This is useful for error-state development and
deterministic browser tests.

### 8. Run browser, visual, and accessibility tests with realistic data

An automated browser configured to use `lemmingd` can exercise a local
frontend under the real hostname while its data continues to come from a QA or
staging API. This gives visual, accessibility, and end-to-end tests realistic
integration behavior without publishing the frontend build first. Keep test
data controlled: remote API actions are not sandboxed by `lemmingd`.

## How rules work

Each domain can appear in only one rule. For a configured domain, routing is:

1. If the path starts with an `exclude_paths` prefix, use the real server.
2. Otherwise, if it starts with an `include_paths` prefix, use the configured
   target.
3. Otherwise, use the real server.

Domains are exact hostnames, matched case-insensitively; wildcard domains are
not supported. Paths are case-sensitive string prefixes. A configured HTTPS
domain is intercepted so that `lemmingd` can inspect its path, including paths
that ultimately continue to the real server. HTTPS domains with no rule are
tunneled without interception.

Targets must be written as `host:port`. Targets use HTTP by default; a target
on port `443` uses HTTPS. Use a hostname that matches the target certificate:
for example, use `giow1091.siteground.us:443` rather than its IP address when
that is the certificate name. Lemmingd still sends the browser-visible hostname
in the HTTP `Host` header, so the target can select the intended virtual host.
Loopback targets are required by default. `allow_remote_targets` permits
non-loopback targets, but should be enabled only when that additional access is
intentional.

## Multiple applications or dev servers

Use separate rules for separate domains:

```json
{
  "version": 1,
  "listen": "127.0.0.1:18080",
  "rules": [
    {
      "target": "127.0.0.1:5173",
      "domains": ["app.qa.example.com"],
      "include_paths": ["/"],
      "exclude_paths": []
    },
    {
      "target": "127.0.0.1:3000",
      "domains": ["admin.qa.example.com"],
      "include_paths": ["/admin/"],
      "exclude_paths": []
    }
  ]
}
```

Several domains can share one target by listing them in the same rule.

## Troubleshooting

- **A `broken pipe` warning appears while browsing:** a browser commonly
  cancels speculative, navigated-away, or closed connections. This warning is
  normally harmless when the requested page still loads; investigate it only
  when it accompanies a reproducible failed request.

- **The proxy page opens, but the site is unchanged:** opening port `18080`
  directly is only a health check. Configure the browser's proxy and then open
  the real HTTPS hostname.
- **The browser reports an untrusted certificate:** import this `lemmingd`
  instance's `ca.crt`, restart the browser, and make sure an old CA with the
  same name was not selected.
- **A route still goes to the real server:** check exclusion order and remember
  that path prefixes and trailing slashes are significant.
- **The local dev server returns 404:** the original path is preserved. The dev
  server must handle that path, including SPA fallback routes if required.
- **The page loads but hot reload fails:** make the HMR client use the real
  hostname with `wss://`, and ensure its path is routed to the local server.
- **Some assets are still remote:** inspect their absolute hostnames. Each
  additional asset hostname needs its own rule.
- **The dev server is on another machine or container address:** loopback is
  required unless `allow_remote_targets` is set to `true`. Consider port
  forwarding it to loopback instead.
- **A migration target fails TLS validation:** use a target hostname covered by
  its certificate instead of the target IP address.
- **A migration target serves its default site:** configure the candidate host
  to accept the original public hostname sent in the HTTP `Host` header.
- **A third-party domain fails only with command-line tests:** unconfigured
  domains are tunneled and use their public CA. A `curl --cacert` file that
  contains only the lemmingd CA is intended for configured/intercepted domains.

## Security boundaries

- Keep the listener on `127.0.0.1`. Setting `allow_remote_clients` does not add
  authentication.
- Trust `ca.crt` only in clients intentionally using this proxy.
- Never share, publish, or import `ca.key`.
- Remove the CA from trust stores when it is no longer needed.
- Treat production sessions as production access: requests, cookies, and API
  actions are real.
