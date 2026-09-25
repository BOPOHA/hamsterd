# Using hamsterd

`hamsterd` is a local HTTP/HTTPS forward proxy that caches safe, public `GET`
responses on disk. Point a build tool, package manager, browser, or other HTTP
client at it; the first eligible download is fetched normally and later
requests for the same URL can be served from the local cache.

It is intended for development downloads, not authenticated application
traffic and not as a general-purpose shared proxy.

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
   header or the `hamsterd` log to see `MISS`, `HIT`, or `BYPASS`.

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

## Common use cases

### 1. Repeated clean builds

Dependency archives and other public build inputs often have stable URLs and
are downloaded again after cleaning a workspace. `hamsterd` can keep eligible
responses locally, reducing build time and bandwidth.

### 2. Recreating containers or development VMs

Ephemeral environments repeatedly fetch the same SDKs, archives, and package
metadata. If their HTTP client uses the host proxy and trusts its CA, they can
reuse the host's persistent cache. Exposing the proxy beyond loopback requires
explicit configuration and network access controls.

### 3. Large public test fixtures and toolchains

Repeated downloads of browser binaries, compilers, public datasets, or test
fixtures can be served locally after the first successful fetch, provided each
object is below `max_object_mib` and its response is cacheable.

### 4. Slow or metered development connections

Caching avoids transferring unchanged public resources repeatedly. It can also
help during a temporary outage while an entry remains present and unexpired,
but `hamsterd` is not an offline mirror and should not be relied on as one.

## What is and is not cached

`hamsterd` caches only complete `GET` responses with status `200`. It bypasses
requests containing:

- `Authorization`, `Cookie`, or `Range` headers;
- `Cache-Control: no-cache` or `Cache-Control: no-store`;
- credentials embedded in the URL.

It also bypasses responses containing:

- `Set-Cookie`, `Vary`, or `Content-Range` headers;
- `Cache-Control: private`, `no-cache`, or `no-store`;
- a non-`200` status;
- an object larger than `max_object_mib`.

If a server does not declare its response size, `hamsterd` can discover that it
is too large only while streaming it. That request can report `MISS`, discard
the partial cache file, and report `MISS` again next time.

Origin `Cache-Control: max-age` and `Expires` values are honored. If neither is
present, `default_ttl` is used. The cache key contains the HTTP method and full
URL, including its query string.

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
- **The cache does not reduce a build's downloads:** confirm that the build tool
  honors the proxy variables and uses the trust store in which the CA was
  installed.
- **The proxy page works but downloads do not use it:** direct access to port
  `8080` is only a health check. The downloading client still needs proxy
  configuration.

## Security boundaries

`hamsterd` intercepts every HTTPS hostname requested through it. Its CA can
issue a certificate for any hostname, so protect `ca.key` and keep the proxy on
loopback. Setting `allow_remote_clients` to `true` permits a non-loopback
listener but does not add authentication.

Trust the CA only in intended development clients, remove it when finished,
and never share, publish, or import `ca.key`.
