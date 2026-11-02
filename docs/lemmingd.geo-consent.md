# Test geo-dependent cookie consent without a VPN

Many sites load a Consent Management Platform (CMP) as a third-party script,
for example `https://cmp.example-cmp.com/CUSTOMER_ID/CONFIG_ID/cmp.js`. The CMP
uses the IP address of the visitor to find the region of the visitor. Then it
applies the consent rules for that region. For example, EU countries use
opt-in rules, and some US states use opt-out rules. These rules control when
the tag manager, analytics, and ad pixels load: before or after consent.

A developer outside a region cannot see the result for visitors in that
region. This procedure uses `lemmingd` to replace only the CMP script with a
copy for a different region. The site and all other CMP requests stay real.

The browser receives:

| Request | Served by |
| --- | --- |
| `https://www.example.com/` and its assets | Real site |
| `https://cmp.example-cmp.com/CUSTOMER_ID/CONFIG_ID/cmp.js` | Local server, changed region |
| All other `cmp.example-cmp.com` paths | Real CMP |
| Tag manager, analytics, and ad requests | Their real servers |

Use this procedure only on sites that you have authorization to test. It
changes only the content in your configured browser. It does not change the
real site or the CMP configuration.

## Why the usual methods do not work

- **Changed client-IP headers have no effect.** The CMP edge uses the IP
  address of the real connection. Requests with different `X-Forwarded-For`,
  `X-Real-IP`, or `True-Client-IP` values get the same response, byte for
  byte:

  ```sh
  cmp_url="https://cmp.example-cmp.com/CUSTOMER_ID/CONFIG_ID/cmp.js"
  curl -s "$cmp_url" | md5sum    # macOS: md5
  for header in X-Forwarded-For X-Real-IP True-Client-IP; do
    for ip in 203.0.113.10 198.51.100.20; do
      printf '%-16s %-14s ' "$header" "$ip"
      curl -s -H "$header: $ip" "$cmp_url" | md5sum
    done
  done
  ```

  Replace the two documentation addresses with public addresses in the
  regions that you want to test. All lines show the same hash as the first
  request. Sometimes two plain requests also give different hashes, for
  example when the script contains a timestamp. In this case, compare the
  region values from [step 1](#1-find-the-region-values) instead.
- **Commercial VPNs** frequently have no exit in a specific state or region.
  Also, their IP ranges can show a different location.
- **Manual cloud-browser sessions** do not support scripts. You cannot easily
  repeat them after each change to the CMP configuration.
- **Changed values in the DevTools console** change only the displayed value.
  An example is a new getter for the region property. At that time, the CMP
  has already applied its rules.

## The source of the region value

The CMP server finds the region. It puts the region in the CMP script
response, or in the response of a separate geo endpoint. Thus, a downloaded
`cmp.js` contains the region of your connection. For example, a visitor in
North Holland gets `"jurisdiction":"nl-nh"` and `"countryCode":"NL"`. When you
change these values in a local copy, the CMP applies the rules of a different
region in your browser.

Region codes usually follow
[ISO 3166-2](https://www.iso.org/iso-3166-country-codes.html). A code has an
ISO 3166-1 alpha-2 country code, a hyphen, and a subdivision code. CMPs
frequently use lowercase letters:

| Code | Region |
| --- | --- |
| `us-fl` | Florida, USA |
| `us-ca` | California, USA |
| `de-by` | Bavaria, Germany |
| `fr-idf` | Île-de-France, France |
| `es-ct` | Catalonia, Spain |
| `it-25` | Lombardy, Italy |
| `nl-nh` | North Holland, Netherlands |
| `pl-14` | Masovia, Poland |

Some CMPs use only the country code for EU visitors, for example `de`,
because GDPR applies to the full country. These CMPs use a subdivision code
only for US states. Use the format that is in your downloaded file.

## Prerequisites

Start `lemmingd` one time. It creates its configuration and its CA. Then stop
it with `Ctrl+C`:

```sh
lemmingd
```

This procedure uses `lemmingd firefox URL`. This command requires NSS
`certutil`. See
[Launch an isolated Firefox profile](lemmingd.md#launch-an-isolated-firefox-profile).
You can also use a dedicated browser that you configure manually. See
[Installing the local CA](ca-installation.md).

Set the CMP script path. Replace `CUSTOMER_ID` and `CONFIG_ID` with the values
from the `<script src=...>` tag of the site:

```sh
demo_dir="$HOME/tmp/lemmingd-geo-consent"
cmp_path="/CUSTOMER_ID/CONFIG_ID/cmp.js"
mkdir -p "$demo_dir"
```

## 1. Find the region values

Download the real script one time. Then show the text around the region
values:

```sh
curl -s "https://cmp.example-cmp.com$cmp_path" -o "$demo_dir/cmp.js"
grep -oiE '.{0,40}(jurisdiction|countryCode).{0,60}' "$demo_dir/cmp.js"
```

Write down the exact spelling, quotes, and spaces of each value, for example
`"jurisdiction":"nl-nh"` and `"countryCode":"NL"`. Property names are
different between CMPs. If there is no match, search the file for the region
code of your connection. If the script does not contain a region code, see
the caveat about separate geo endpoints in [Caveats](#caveats).

## 2. Change the script and serve it

The local copy must use the same path as the real script, because `lemmingd`
does not change the path. Replace the full value with its quotes, not only
the code. A simple `sed 's/NL/US/g'` also changes unrelated text, for example
`INLINE` or names that contain `NL`. This can break the script.

```sh
patch_region() {
  mkdir -p "$demo_dir/mock$(dirname "$cmp_path")"
  sed -e "s/\"jurisdiction\":\"nl-nh\"/\"jurisdiction\":\"$1\"/g" \
      -e "s/\"countryCode\":\"NL\"/\"countryCode\":\"$2\"/g" \
      "$demo_dir/cmp.js" > "$demo_dir/mock$cmp_path"
  grep -oiE '.{0,40}(jurisdiction|countryCode).{0,60}' "$demo_dir/mock$cmp_path"
}
patch_region us-fl US
```

Change the two `sed` expressions to agree with the exact text from step 1.
The `grep` output must show only the target region. To make sure that no
other text changed, compare the two files value by value:

```sh
tr ',' '\n' < "$demo_dir/cmp.js" > "$demo_dir/cmp.orig.txt"
tr ',' '\n' < "$demo_dir/mock$cmp_path" | diff "$demo_dir/cmp.orig.txt" -
```

Start the local server in a second terminal. Keep it running:

```sh
python3 -m http.server 8000 --bind 127.0.0.1 \
  --directory "$HOME/tmp/lemmingd-geo-consent/mock"
```

## 3. Send only the CMP script to the local server

Make a copy of the current configuration, if it exists. Then replace the
configuration with this rule. On macOS, set
`lemming_config="$HOME/Library/Application Support/lemmingd/config.json"`
instead of the Linux path in the first line:

```sh
lemming_config="${XDG_CONFIG_HOME:-$HOME/.config}/lemmingd/config.json"
if [ -f "$lemming_config" ]; then
  cp "$lemming_config" "$lemming_config.before-geo-consent"
fi
cat > "$lemming_config" <<EOF
{
  "version": 1,
  "listen": "127.0.0.1:18080",
  "rules": [
    {
      "target": "127.0.0.1:8000",
      "domains": ["cmp.example-cmp.com"],
      "include_paths": ["$cmp_path"]
    }
  ]
}
EOF
```

The [example configuration](../examples/lemmingd-geo-consent.json) contains
the same rule.

The rule operates as follows. See also
[How rules work](lemmingd.md#how-rules-work).

- `lemmingd` intercepts only `cmp.example-cmp.com`. There is no rule for
  `www.example.com`, so its HTTPS traffic goes through a tunnel without
  changes.
- Each `include_paths` entry is a case-sensitive prefix of the URL path. The
  match does not use the query string. Thus, `lemmingd` also sends
  `cmp.js?v=123` to the local server. The local server gets the same path and
  query.
- The entry is a prefix, so it also matches longer paths, for example
  `/CUSTOMER_ID/CONFIG_ID/cmp.js.map`. The local server returns `404` for
  these paths. This is not a problem for a source map. If other CMP files
  have this prefix and must come from the real CMP, add their paths to
  `exclude_paths`.
- All other paths on `cmp.example-cmp.com` go to the real CMP, for example
  consent logs, vendor lists, and translations.
- This rule does not need `exclude_paths`. `lemmingd` examines exclusions
  first. Thus, an exclusion that is a prefix of the script path, for example
  `/` or `/CUSTOMER_ID/`, sends the script to the real CMP again. Use an
  exclusion only to keep a sub-path of a wider inclusion on the real CMP. For
  example, include `/CUSTOMER_ID/CONFIG_ID/` and exclude
  `/CUSTOMER_ID/CONFIG_ID/vendor-list.json`.
- A domain can be in only one rule. If your configuration already has a rule
  for `cmp.example-cmp.com`, add the path to that rule.

If `control_api` is on, a coding agent can add the same rule. It uses the
`add_rule` tool of
[`lemmingd mcp`](lemmingd.md#control-lemmingd-from-coding-agents-mcp).

## 4. Open the site

Start `lemmingd` with its isolated Firefox profile:

```sh
lemmingd firefox https://www.example.com/
```

The `lemmingd` terminal shows the routed script:

```text
session=4 route host=cmp.example-cmp.com path=/CUSTOMER_ID/CONFIG_ID/cmp.js target=127.0.0.1:8000
```

The managed profile keeps cookies and storage between runs. Thus, a stored
consent can hide the behavior of a first visit. For each region, use a new
private window in this Firefox instance (`Ctrl+Shift+P`, or `Cmd+Shift+P` on
macOS). A private window uses the same proxy and CA, but it has no stored
consent. For a full reset, close Firefox and `lemmingd`. Then delete the
`firefox-profile` directory that the startup log shows. The next
`lemmingd firefox` command creates it again.

## 5. Verify the region and record the tag behavior

In the Console, read the region value. Use the documented JavaScript API of
the CMP or the property from step 1. Make sure that the value is the target
region, for example `jurisdiction === "us-fl"`. If the value is still the
region of your connection, the browser used a cached copy or the real copy.
Look for the route line in the `lemmingd` terminal. Then open a new private
window.

Open the Network panel and select *Persist Logs*. Reload the page. Record the
tracking requests at each stage. To show fewer requests, use a filter for the
tag manager, analytics, and ad hostnames:

| Region | Before consent | After Accept | After Deny |
| --- | --- | --- | --- |
| `us-fl` | | | |
| `us-ca` | | | |
| `de-by` | | | |
| `fr-idf` | | | |

For each region, change the script again and open a new private window. Only
the served file changes, so you do not need to restart `lemmingd`:

```sh
patch_region de-by DE
patch_region fr-idf FR
```

Compare the EU regions with the US states to find the differences between
opt-in and opt-out rules. In an opt-in region, the site must not send
tracking requests before Accept.

## Caveats

- **This procedure tests how the tags of your site react to the rules of a
  region.** It does not prove that real visitors from that region get that
  code. Do one check with a real IP address in that region.
- **The local copy becomes out of date.** Download and change the script
  again after each publication of the CMP configuration. If you do not, the
  test uses old rules.
- **Subresource Integrity blocks the changed file.** If the `<script>` tag of
  the site has an `integrity=` attribute, the browser does not run the changed
  script. Use a test environment that loads the CMP without this attribute.
- **Scripts with `crossorigin` need CORS headers.** If the `<script>` tag has a
  `crossorigin` attribute, the local server must send a correct
  `Access-Control-Allow-Origin` header. `python3 -m http.server` does not send
  this header.
- **Use a new profile or a private window for each run.** If you do not,
  stored consent can hide the behavior of a first visit.
- **Some CMPs get the region from a separate endpoint.** In this case, the
  script does not contain the region. To find the endpoint, search all loaded
  files in DevTools for the region code of your connection. In Firefox, use
  `Ctrl+Shift+F` (`Cmd+Shift+F` on macOS). In Chrome, use `Ctrl+Shift+F`
  (`Cmd+Option+F` on macOS). Then serve and route that path, instead of the
  script or together with it.
- **Other requests still use the real IP address of your connection.**
  Consent logs, the tag manager, and analytics record the real location.
  Server-side tagging can also find the region itself.
- **The vendor possibly does not support changes to its script.** Use this
  procedure only for local tests. Do not use it in production.

## Restore the previous configuration

After you stop `lemmingd`, restore the copy if it exists:

```sh
lemming_config="${XDG_CONFIG_HOME:-$HOME/.config}/lemmingd/config.json"
if [ -f "$lemming_config.before-geo-consent" ]; then
  mv "$lemming_config.before-geo-consent" "$lemming_config"
fi
```
