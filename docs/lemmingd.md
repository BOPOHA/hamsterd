# Using lemmingd

`lemmingd` lets a browser keep using a real HTTPS URL while selected requests
are served by a development server on your machine. The browser still sees
`https://app.example.com`; `lemmingd` decides which requests go to the real
server and which go to `http://127.0.0.1:<port>`.

This is useful when a frontend engineer needs real QA or production-like APIs,
cookies, redirects, and HTTPS without running the complete backend locally.

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

## Main example: local frontend, real API

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

The path and query string are preserved. Exclusions are checked before
inclusions, which is why the broad `/` rule does not capture `/api/`.

Path matching is literal prefix matching. `/api/` does not match the exact path
`/api`; add both if the application uses both forms.

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
- The local server receives the rewritten target as its `Host`, such as
  `127.0.0.1:5173`, while browser headers such as `Origin` and `Referer` can
  contain the real HTTPS hostname. Account for that in dev-server host and
  origin checks.

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

## Common use cases

### 1. Local frontend with a remote API

Use the configuration above: include `/` and exclude `/api/`. The HTML,
JavaScript, CSS, and other frontend routes come from `npm run dev`, while API
requests continue to QA, staging, or production.

This is useful for reproducing frontend-only bugs with realistic remote data.
Prefer a test account and a non-production environment; the remote API remains
fully live.

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
developed locally. This is handy for checking a response-shape change against a
deployed frontend.

### 3. Test CORS with HTTPS and a real remote API

Use different browser-visible hostnames for the frontend and API. For example,
route all of `https://app.qa.example.com/` to the local frontend, but do not add
a rule for `api.qa.example.com`:

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
    }
  ]
}
```

Configure the frontend to call `https://api.qa.example.com`. The browser sees
the frontend origin as `https://app.qa.example.com`, the API connection remains
remote, and normal browser CORS checks apply.

If both frontend and API use `https://example.com` with paths such as `/` and
`/api/`, they are the same origin. That setup is useful, but it does **not**
test CORS.

### 4. Replace only selected static assets

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

This is a small, low-disruption way to verify an asset fix against the real
page.

### 5. Reproduce HTTPS-only browser behavior

Serve the local application through a deployment-like HTTPS origin to inspect
Secure cookies, SameSite behavior, service workers, OAuth redirects, or code
that behaves differently in a secure context. The browser connects to
`https://app.qa.example.com`; `lemmingd` terminates that TLS connection and
forwards selected paths to the local HTTP server.

This reproduces the browser-visible scheme and hostname, but it does not
reproduce every property of the deployed edge, CDN, or production TLS setup.

## How rules work

Each domain can appear in only one rule. For a configured domain, routing is:

1. If the path starts with an `exclude_paths` prefix, use the real server.
2. Otherwise, if it starts with an `include_paths` prefix, use `http://target`.
3. Otherwise, use the real server.

Domains are exact hostnames, matched case-insensitively; wildcard domains are
not supported. Paths are case-sensitive string prefixes. A configured HTTPS
domain is intercepted so that `lemmingd` can inspect its path, including paths
that ultimately continue to the real server. HTTPS domains with no rule are
tunneled without interception.

Targets must be written as `host:port`. They use HTTP, and loopback targets are
required by default. `allow_remote_targets` permits non-loopback targets, but
should be enabled only when that additional access is intentional.

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
