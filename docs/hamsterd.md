# Using hamsterd

**Download an eligible public artifact once; reuse it across builds and
development machines.**

`hamsterd` is an HTTP/HTTPS forward proxy that caches safe, public `GET`
responses on disk. Point a build tool, package manager, browser, or other HTTP
client at it; the first eligible download is fetched normally and later
requests for the same URL can be served from the local cache. Because the
cache is at the HTTP layer, it can work across tools without adding a separate
filesystem-cache integration to every build system.

For a repeatable Terragrunt example, including selective domain interception,
client trust, timing, and cache cleanup, see
[Cache Terraform downloads during Terragrunt runs](hamsterd.howto.md). In that
example, the warm-cache run took 21.394 seconds instead of 2 minutes 5.839
seconds without the proxy—almost six times faster.

It is intended for local or controlled-network development downloads, not
authenticated application traffic and not as an unrestricted general-purpose
proxy.

## The basic workflow

1. Start `hamsterd`:

   ```sh
   hamsterd
   ```

   On first start it creates a configuration, cache directory, and unique CA.
   The default proxy address is `127.0.0.1:8080`.

2. Trust the generated CA only in the client that will use the proxy. See
   [Installing the local CA](ca-installation.md).

3. Configure the client to use this HTTP proxy for both HTTP and HTTPS:

   ```text
   http://127.0.0.1:8080
   ```

   The proxy URL itself starts with `http://` even when the requested download
   uses HTTPS.

4. Run the build or download normally. Inspect the `X-Hamsterd-Cache` response
   header or the `hamsterd` log to see `MISS`, `HIT`, `STALE`, or `BYPASS`.

For HTTPS, the log also reports `action=intercept` for allowlisted connections
and `action=tunnel` for other connections. A tunnel preserves end-to-end TLS,
so `hamsterd` can log its hostname but cannot see individual request paths.

Opening `http://127.0.0.1:8080/` directly only shows the status page; it does
not configure the browser or operating system.

## Quick command-line test

Use a local origin so the test does not depend on a public site's cache
headers. From the repository directory, start an origin server in a second
terminal:

```sh
python3 -m http.server 8000 --bind 127.0.0.1
```

Keep `hamsterd` running, then execute these commands twice in another terminal:

```sh
TEST_URL='http://127.0.0.1:8000/README.md?hamsterd-doc-test=1'
curl --noproxy '' --proxy http://127.0.0.1:8080 \
  -D - "$TEST_URL" -o /dev/null
```

The empty `--noproxy` value prevents an existing `NO_PROXY` setting from
bypassing `hamsterd`. Change the query value if that test URL is already in the
cache.

For an eligible response, the first request has:

```text
X-Hamsterd-Cache: MISS
```

and the second has:

```text
X-Hamsterd-Cache: HIT
```

`STALE` means the origin's freshness lifetime has ended, but its
`stale-while-revalidate` window permits immediate reuse. `hamsterd` returns the
stored response and refreshes it in the background.

`BYPASS` means the request or response was known to be ineligible before its
body was streamed. `MISS` means there was no usable cached copy and `hamsterd`
attempted to capture the response. A `MISS` is retained only if the entire body
is read, stays within the size limit, and is written successfully.

## Use it with command-line tools

Many tools understand the standard proxy environment variables:

```sh
export HTTP_PROXY=http://127.0.0.1:8080
export HTTPS_PROXY=http://127.0.0.1:8080
export NO_PROXY=localhost,127.0.0.1

export http_proxy="$HTTP_PROXY"
export https_proxy="$HTTPS_PROXY"
export no_proxy="$NO_PROXY"
```

These variables select the proxy; they do not make the client trust its CA.
Install the CA in the relevant trust store or configure the individual tool to
use `~/.config/hamsterd/ca.crt`. Do not disable TLS verification.

Unset the variables when finished:

```sh
unset HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy
```

## Strong use cases

### 1. Repeated builds and infrastructure initialization

Clean builds and repeated Terraform or Terragrunt initialization often fetch
the same provider archives, SDKs, compilers, browser binaries, and dependency
metadata. `hamsterd` keeps eligible responses outside the workspace, reducing
both elapsed time and internet traffic after the first download.

This is especially useful when a tool's own cache is disabled, inconvenient to
share, or removed along with the build directory.

### 2. Ephemeral CI runners, containers, and development VMs

Disposable environments normally start with an empty filesystem cache. If the
client uses a persistent `hamsterd` instance and trusts its CA, it can reuse
downloads from earlier jobs even though the runner, container, or VM itself is
new.

### 3. One development cache for several tools

A team may otherwise need separate cache directories and CI configuration for
Terraform providers, browser test binaries, language toolchains, public data,
and test fixtures. Clients that support an HTTP proxy can share one bounded
cache instead. Each object must still be below `max_object_mib`, and the origin
response must be cacheable.

### 4. A trusted development LAN, lab, or build farm

One `hamsterd` instance can serve multiple authorized development machines.
After a cache miss stores a response, subsequent requests reuse it over the
local network while the entry remains usable. This is useful for build farms,
workshops, classrooms, test labs, and teams behind a slow or metered uplink.

This mode is not enabled by default. Bind to a private interface only after
setting `allow_remote_clients`, restrict access with host or network firewall
rules, and preferably configure an exact `domains` allowlist. Each client must
trust the server's public `ca.crt`; never copy `ca.key` to a client. There is no
built-in client authentication, so do not expose the listener to the internet
or an untrusted LAN.

### 5. Slow, metered, VPN, or egress-charged connections

Caching avoids transferring unchanged public resources repeatedly and can
reduce bandwidth or egress cost. It can also help during a temporary outage
while an entry remains usable, but `hamsterd` is not an offline mirror and
should not be relied on as one.

## Poor fits

`hamsterd` is deliberately conservative. It is not a replacement for an
authenticated artifact repository, a content-addressed build cache, or an
offline mirror. Downloads that use credentials, cookies, range requests,
private responses, unsupported `Vary` fields, or non-`200` responses are
bypassed. A cache hit reuses the origin response according to HTTP freshness
rules; it does not make an artifact reproducible or immutable.

## What is and is not cached

`hamsterd` caches only complete `GET` responses with status `200`. It bypasses
requests containing:

- `Authorization`, `Cookie`, or `Range` headers;
- `Cache-Control: no-cache` or `Cache-Control: no-store`;
- credentials embedded in the URL.

It also bypasses responses containing:

- `Set-Cookie` or `Content-Range` headers;
- a `Vary` field other than `Accept-Encoding` or `Origin`;
- `Cache-Control: private`, `no-cache`, or `no-store`;
- a non-`200` status;
- an object larger than `max_object_mib`.

If a server does not declare its response size, `hamsterd` can discover that it
is too large only while streaming it. That request can report `MISS`, discard
the partial cache file, and report `MISS` again next time.

Origin `Cache-Control: max-age`, `stale-while-revalidate`, and `Expires` values
are honored. If neither freshness value is present, `default_ttl` is used. The
cache key contains the HTTP method, full URL including its query string, and
the request's `Accept-Encoding` and `Origin` values so those supported variants
cannot be mixed.

This conservative policy prevents personalized or partial responses from being
replayed as public downloads. It also means authenticated package registries
and many ordinary web pages will show `BYPASS`.

## Configuration

The default file is:

```text
~/.config/hamsterd/config.json
```

If `XDG_CONFIG_HOME` is set, it is stored below that directory instead. These
are Linux paths; on other platforms, use the path printed in the startup log.
A typical configuration is:

```json
{
  "version": 1,
  "listen": "127.0.0.1:8080",
  "cache": {
    "directory": "",
    "max_size_mib": 1024,
    "max_object_mib": 256,
    "default_ttl": "1h"
  }
}
```

The optional top-level `domains` list limits caching and HTTPS interception to
exact hostnames. HTTPS traffic for every other hostname is tunneled without TLS
interception; plain HTTP traffic is forwarded without caching. An omitted or
empty list preserves the default behavior of caching and intercepting all
hosts.

An empty `directory` uses `~/.cache/hamsterd/`, or the equivalent below
`XDG_CACHE_HOME`. `max_size_mib` limits the complete cache, and least-recently
used entries are removed when necessary. `default_ttl` uses Go duration syntax,
such as `30m`, `1h`, or `24h`.

Restart `hamsterd` after editing its configuration.

## Troubleshooting

- **Certificate errors:** install this `hamsterd` instance's CA in the client
  that uses the proxy. `lemmingd` has a different CA.
- **Everything says `BYPASS`:** check the request and response against the
  conservative cache rules above. Cookies and authorization are common causes.
- **A repeated request says `MISS`:** confirm the exact URL is unchanged, the
  first response body was downloaded completely, the entry has not expired,
  and a streaming response did not exceed `max_object_mib`.
- **A repeated request says `STALE`:** the origin's `max-age` has elapsed, but
  `stale-while-revalidate` permits immediate reuse while `hamsterd` refreshes
  the entry in the background.
- **The cache does not reduce a build's downloads:** confirm that the build tool
  honors the proxy variables and uses the trust store in which the CA was
  installed.
- **The proxy page works but downloads do not use it:** direct access to port
  `8080` is only a health check. The downloading client still needs proxy
  configuration.

## Security boundaries

Without a `domains` allowlist, `hamsterd` intercepts every HTTPS hostname
requested through it. Its CA can issue a certificate for any hostname, so
protect `ca.key` and keep the proxy on loopback. Setting
`allow_remote_clients` to `true` permits a non-loopback listener but does not
add authentication.

Trust the CA only in intended development clients, remove it when finished,
and never share, publish, or import `ca.key`.
