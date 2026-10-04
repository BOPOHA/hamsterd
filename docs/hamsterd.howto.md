# Cache Terraform downloads during Terragrunt runs

This copy-paste example uses `hamsterd` to cache large HashiCorp provider
downloads and eligible Terraform Registry metadata during repeated
`terragrunt init` runs. It intercepts only the two relevant hosts:

- `registry.terraform.io`
- `releases.hashicorp.com`

GitHub module clones, AWS backend requests, and all other traffic pass through
the proxy without TLS interception or caching. This matters because a
Terragrunt `tfr:///` source is resolved through the Terraform Registry and then
usually cloned from GitHub. Git's smart HTTP exchange uses requests that
`hamsterd` does not cache, so GitHub is not included in the domain list.

## 1. Configure hamsterd

Start `hamsterd` once, then stop it with `Ctrl+C`. This creates its unique CA
and `$HOME/.config/hamsterd/config.json` on Linux.

Replace that configuration with:

```json
{
  "version": 1,
  "listen": "127.0.0.1:8080",
  "domains": [
    "registry.terraform.io",
    "releases.hashicorp.com"
  ],
  "cache": {
    "directory": "",
    "max_size_mib": 4096,
    "max_object_mib": 512,
    "default_ttl": "24h"
  }
}
```

An empty `cache.directory` uses
`${XDG_CACHE_HOME:-$HOME/.cache}/hamsterd`. The 512 MiB per-object limit leaves
room for large provider archives. The domain list uses exact hostnames; it does
not imply subdomains or wildcards.

Start `hamsterd` and leave it running:

```sh
hamsterd
```

## 2. Run a cold initialization

Change to a Terragrunt module that uses a public registry module and provider.
The example below assumes the directory already contains `terragrunt.hcl` and
`.terraform.lock.hcl`.

Remove only Terragrunt's generated working directory:

```sh
rm -rf -- .terragrunt-cache
```

Then time the first run through `hamsterd`:

```sh
https_proxy=http://127.0.0.1:8080 \
SSL_CERT_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/hamsterd/ca.crt" \
time terragrunt init
```

The first eligible request is logged as `cache=MISS`. Connections to GitHub and
AWS are logged with `action=tunnel` because their hostnames are not in
`domains`, so `hamsterd` leaves their certificates unchanged. The CA override
applies only to this command and requires no system-wide installation or
`sudo`.

Typical log lines include:

```text
session=8 method=CONNECT host=registry.terraform.io action=intercept
session=9 method=GET host=registry.terraform.io path=/.well-known/terraform.json status=200 cache=MISS
session=12 method=CONNECT host=github.com action=tunnel
```

For a tunneled connection, `hamsterd` can report the hostname but cannot see
the encrypted request paths or responses.

## 3. Repeat with a warm hamsterd cache

Delete Terragrunt's generated directory again, but keep the `hamsterd` cache:

```sh
rm -rf -- .terragrunt-cache
```

Run the same timed command:

```sh
https_proxy=http://127.0.0.1:8080 \
SSL_CERT_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/hamsterd/ca.crt" \
time terragrunt init
```

The important result is a `HIT` for the provider ZIP:

```text
method=GET host=releases.hashicorp.com path=/terraform-provider-aws/6.65.0/terraform-provider-aws_6.65.0_linux_amd64.zip status=200 cache=HIT
```

That large archive is the main download this example is intended to cache. Its
checksum and signature files should normally become `HIT` entries too.

The provider-version index has `max-age=30, stale-while-revalidate=1800`.
During that 30-minute stale window, `hamsterd` returns it immediately as
`cache=STALE` and refreshes it in the background. The module download lookup
returns status `204`, so it remains `BYPASS`; `hamsterd` caches only status
`200` responses.

The overall second run still includes a fresh GitHub clone and Terraform's
normal initialization work. Compare the logs as well as the total elapsed
time; `hamsterd` is not a replacement for Terragrunt's own download cache or
Terraform's optional provider plugin cache.

### Example results

One test with `terraform-aws-modules/route53/aws` version `4.1.0` and
`hashicorp/aws` version `6.65.0` produced:

| Run | hamsterd cache | Elapsed time |
| --- | --- | ---: |
| Direct | Not used | 2m 5.839s |
| First proxied run | Cold | 2m 19.056s |
| Second proxied run | Warm | 21.394s |

The warm run was almost six times faster than the direct run because the large
provider ZIP was served locally. The cold proxied run can be slightly slower
because it downloads and writes the response to the cache simultaneously.
Results vary with network speed, storage, provider size, and Terraform's own
caches.

## 4. Clear both caches and repeat from cold state

Stop `hamsterd` before removing its cache. From the Terragrunt module directory,
run:

```sh
rm -rf -- .terragrunt-cache
rm -rf -- "${XDG_CACHE_HOME:-$HOME/.cache}/hamsterd"
```

Restart `hamsterd`, then repeat the command from step 2. Removing the cache
directory does not remove `config.json`, `ca.crt`, or `ca.key`; those remain in
`${XDG_CONFIG_HOME:-$HOME/.config}/hamsterd`.

If `cache.directory` contains a custom path, remove that directory instead.
Do not delete `.terraform.lock.hcl`: keeping the lock file ensures both runs
select the same provider versions and verify the same checksums.
