# Replace a static site's theme locally with lemmingd

This copy-paste example changes the theme of
[Jekyll's website](https://jekyllrb.com/) without changing the site or hosting
a local copy of its HTML. Jekyll's site is static and loads its stylesheet from
`/css/screen.css`, so `lemmingd` can replace only the `/css/` directory.

The browser will receive:

| Request | Served by |
| --- | --- |
| `https://jekyllrb.com/` | Real Jekyll website |
| `https://jekyllrb.com/css/screen.css` | Local server |
| `/img/`, `/fonts/`, and all other paths | Real Jekyll website |

Use this only with sites you are authorized to test. This example changes only
what you see in your configured browser; it does not modify the real website.

## Prerequisites

Start `lemmingd` once so it creates its configuration and CA:

```sh
lemmingd
```

Stop it with `Ctrl+C`. Then install its CA in a dedicated development browser
and configure that browser to use `127.0.0.1:18080` for both HTTP and HTTPS.
See [Installing the local CA](ca-installation.md) for the browser steps.

## 1. Download and change the theme

Download the home page and its assets:

```sh
demo_dir="$HOME/tmp/lemmingd-jekyll-demo"
mkdir -p "$demo_dir/jekyllrb.com"
wget --page-requisites --convert-links --adjust-extension \
  --no-host-directories \
  --directory-prefix="$demo_dir/jekyllrb.com" \
  https://jekyllrb.com/
```

Append a deliberately obvious dark purple theme to the downloaded stylesheet:

```sh
demo_css="$HOME/tmp/lemmingd-jekyll-demo/jekyllrb.com/css/screen.css"
printf '%s\n' \
  '' \
  '/* Local lemmingd demo overrides. */' \
  'html body { background: #111827; color: #f9fafb; }' \
  'body .masthead, body footer { background: #6d28d9; }' \
  'body a { color: #fbbf24; }' \
  >> "$demo_css"
```

## 2. Serve the downloaded assets

Run this in a second terminal and leave it running:

```sh
python3 -m http.server 5173 \
  --directory "$HOME/tmp/lemmingd-jekyll-demo/jekyllrb.com"
```

Confirm that the local stylesheet is available:

```sh
curl -I http://127.0.0.1:5173/css/screen.css
```

The response should be `HTTP/1.0 200 OK`.

## 3. Route `/css/` to the local server

Save the existing configuration, if any, and replace it with this demo rule:

```sh
lemming_config="${XDG_CONFIG_HOME:-$HOME/.config}/lemmingd/config.json"
if [ -f "$lemming_config" ]; then
  cp "$lemming_config" "$lemming_config.before-jekyll-demo"
fi
printf '%s\n' \
  '{' \
  '  "version": 1,' \
  '  "listen": "127.0.0.1:18080",' \
  '  "rules": [' \
  '    {' \
  '      "target": "127.0.0.1:5173",' \
  '      "domains": ["jekyllrb.com"],' \
  '      "include_paths": ["/css/"]' \
  '    }' \
  '  ]' \
  '}' \
  > "$lemming_config"
```

Start `lemmingd` again in a third terminal:

```sh
lemmingd
```

## 4. View the result

Open `https://jekyllrb.com/` in the configured browser and perform a hard
reload (`Ctrl+Shift+R`). The page and its content still come from the real
Jekyll site, while the dark purple theme comes from your local stylesheet.

The `lemmingd` terminal should show the intercepted asset:

```text
route host=jekyllrb.com path=/css/screen.css target=127.0.0.1:5173
```

Edit `css/screen.css` again and hard-reload the browser to iterate. To replace
images instead, download the files below `/img/`, serve them under the same
paths locally, and add `/img/` to `include_paths`.

## Restore the previous configuration

After stopping `lemmingd`, restore the backup if one was created:

```sh
lemming_config="${XDG_CONFIG_HOME:-$HOME/.config}/lemmingd/config.json"
if [ -f "$lemming_config.before-jekyll-demo" ]; then
  mv "$lemming_config.before-jekyll-demo" "$lemming_config"
fi
```
