# Installing the local CA

`hamsterd` and `lemmingd` inspect HTTPS traffic for their respective jobs. Each
creates its own private certificate authority (CA) on first start. A browser or
command-line client must trust that CA before it will accept the proxy's HTTPS
certificates.

Only do this on a development machine and only for a proxy you started and
control.

## Choose the correct certificate

The default public certificate paths are:

| Platform | `lemmingd` certificate |
| --- | --- |
| Linux | `${XDG_CONFIG_HOME:-$HOME/.config}/lemmingd/ca.crt` |
| macOS | `~/Library/Application Support/lemmingd/ca.crt` |
| Windows | `%AppData%\lemmingd\ca.crt` |

Replace `lemmingd` with `hamsterd` for that proxy. The startup log always shows
the effective path, including when a command-line override is used. You can
also download the public certificate while the proxy is running:

```sh
curl -o lemmingd-ca.crt http://127.0.0.1:18080/ca.crt
curl -o hamsterd-ca.crt http://127.0.0.1:8080/ca.crt
```

The adjacent `ca.key` file is the private signing key. **Never import, copy,
publish, or share `ca.key`.**

Before trusting a certificate, inspect its subject and fingerprint. Set
`CA_CERT` to the actual path printed by the proxy:

```sh
CA_CERT="${XDG_CONFIG_HOME:-$HOME/.config}/lemmingd/ca.crt"
openssl x509 -in "$CA_CERT" -noout -subject -issuer -fingerprint -sha256
```

The common name should be `lemmingd local CA` (or `hamsterd local CA`). Compare
the fingerprint with the original file if the certificate was copied to a
different client.

## Prefer per-client trust

The narrowest option is safest. For one `curl` command, no installation is
needed:

```sh
curl --proxy http://127.0.0.1:18080 \
  --cacert "${XDG_CONFIG_HOME:-$HOME/.config}/lemmingd/ca.crt" \
  https://app.qa.example.com/
```

Use the equivalent `hamsterd` path and port `8080` for that proxy. Application
options named `--cacert`, `--ca-file`, or `SSL_CERT_FILE` often provide a
similarly narrow alternative. Prefer the application's documented setting;
never use an "insecure" or "skip verification" option as a substitute.

## Firefox

Firefox can keep trust local to one browser profile:

1. Open **Settings**.
2. Open **Privacy & Security**.
3. Under **Certificates**, select **View Certificates**.
4. On the **Authorities** tab, select **Import**.
5. Choose the appropriate `ca.crt` file.
6. Allow it to identify websites, then confirm.
7. Restart Firefox.

To remove it later, return to **Authorities**, select `lemmingd local CA` or
`hamsterd local CA`, and choose **Delete or Distrust**.

Firefox installations managed by an organization or distribution may use the
operating-system trust store instead. `about:policies` shows active enterprise
policies.

### Firefox from the command line

With Firefox closed and the NSS tools installed (`nss-tools` on Fedora or
`libnss3-tools` on Debian/Ubuntu), its profile database can be updated with
`certutil`. Find the profile directory from `about:profiles`, then run:

```sh
certutil -A \
  -d sql:/absolute/path/to/firefox/profile \
  -n "lemmingd local CA" \
  -t "C,," \
  -i "${XDG_CONFIG_HOME:-$HOME/.config}/lemmingd/ca.crt"
```

List or remove that entry with:

```sh
certutil -L -d sql:/absolute/path/to/firefox/profile
certutil -D -d sql:/absolute/path/to/firefox/profile -n "lemmingd local CA"
```

Replace the nickname and path for `hamsterd`. Do not guess the profile path or
modify a running Firefox profile.

## Chrome, Chromium, Brave, and Edge

Open the browser's **Settings**, search for **certificates**, and open **Manage
certificates**. Import `ca.crt` under **Authorities** or **Trusted Root
Certification Authorities**, depending on the operating system. Mark it as
trusted for identifying websites if prompted, then restart the browser.

On Windows and macOS these browsers normally use an operating-system-managed
certificate store. On Linux the exact UI and backing store vary by browser and
distribution. Use the browser UI when you want browser-only trust; use the OS
instructions below when all development clients should trust the CA.

Remove the CA from the same Authorities or Trusted Roots section when done.

## Safari

Safari uses the macOS Keychain trust settings:

1. Open **Keychain Access**.
2. Select the **login** keychain.
3. Drag `ca.crt` into the **Certificates** category, or use **File > Import
   Items**.
4. Open the imported certificate, expand **Trust**, and select **Always Trust**.
5. Close the window and authenticate when macOS asks.
6. Restart Safari.

Delete `lemmingd local CA` or `hamsterd local CA` from Keychain Access when it
is no longer required.

## Linux system trust from the command line

System trust affects more applications than one browser. Use it only when that
scope is intentional.

### Fedora, RHEL, CentOS Stream, and derivatives

```sh
sudo install -m 0644 \
  "${XDG_CONFIG_HOME:-$HOME/.config}/lemmingd/ca.crt" \
  /etc/pki/ca-trust/source/anchors/lemmingd-local-development-ca.crt
sudo update-ca-trust
```

Remove it later with:

```sh
sudo rm /etc/pki/ca-trust/source/anchors/lemmingd-local-development-ca.crt
sudo update-ca-trust
```

### Debian, Ubuntu, and derivatives

```sh
sudo install -m 0644 \
  "${XDG_CONFIG_HOME:-$HOME/.config}/lemmingd/ca.crt" \
  /usr/local/share/ca-certificates/lemmingd-local-development-ca.crt
sudo update-ca-certificates
```

Remove it later with:

```sh
sudo rm /usr/local/share/ca-certificates/lemmingd-local-development-ca.crt
sudo update-ca-certificates --fresh
```

Use `hamsterd` in the source and destination names when installing its CA.
Restart browsers and long-running applications after changing system trust.

## macOS from the command line

Add the CA to the current user's login keychain:

```sh
security add-trusted-cert \
  -r trustRoot \
  -k "$HOME/Library/Keychains/login.keychain-db" \
  "$HOME/Library/Application Support/lemmingd/ca.crt"
```

First display matching certificates and copy the SHA-256 fingerprint of the
exact CA you installed:

```sh
security find-certificate \
  -a -c "lemmingd local CA" -Z \
  "$HOME/Library/Keychains/login.keychain-db"
```

Then remove that exact certificate:

```sh
security delete-certificate \
  -Z PASTE_SHA256_FINGERPRINT_HERE \
  "$HOME/Library/Keychains/login.keychain-db"
```

macOS may ask for authentication. Replace `lemmingd` with `hamsterd` when
working with the other proxy. Using the fingerprint avoids deleting the wrong
certificate if more than one locally generated CA has the same name.

## Windows from PowerShell

Import into the current user's Trusted Root store; administrator access is not
normally required for this per-user store:

```powershell
$ca = Import-Certificate `
  -FilePath "$env:APPDATA\lemmingd\ca.crt" `
  -CertStoreLocation Cert:\CurrentUser\Root
$ca.Thumbprint
```

Keep the displayed thumbprint. Remove that exact certificate later with:

```powershell
Remove-Item "Cert:\CurrentUser\Root\PASTE_THUMBPRINT_HERE"
```

Replace `lemmingd` with `hamsterd` for the other proxy, or use the path printed
by the proxy if it was overridden.

## Configure the browser's proxy

Trust and proxy configuration are separate. After importing the CA, configure
both HTTP and HTTPS proxy traffic:

| Tool | Host | Port |
| --- | --- | --- |
| `lemmingd` | `127.0.0.1` | `18080` |
| `hamsterd` | `127.0.0.1` | `8080` |

In Firefox, use **Settings > General > Network Settings > Manual proxy
configuration** and enable the option to use the HTTP proxy for HTTPS. Chrome,
Chromium, Brave, Edge, and Safari normally use the operating system's proxy
settings.

For a temporary Chromium-family browser session on Linux, the proxy itself can
also be selected at launch:

```sh
chromium --proxy-server=http://127.0.0.1:18080
```

The browser must still trust the CA. Close all existing browser processes first
if the browser reuses an already-running process and ignores the new option.

## Confirm that it works

1. Keep the selected proxy running.
2. Visit a configured HTTPS domain through the browser.
3. Inspect the page certificate. Its issuer should be `lemmingd local CA` or
   `hamsterd local CA`.
4. For `lemmingd`, confirm locally routed requests appear in its terminal log.
5. For `hamsterd`, inspect `X-Hamsterd-Cache` in the response headers.

## Security and cleanup

A trusted local CA can issue certificates for any hostname. Anyone who obtains
its private key can impersonate HTTPS sites to clients that trust the CA.

- Never share or publish `ca.key`.
- Do not install these CAs on machines that do not use the proxy.
- Keep the proxies bound to loopback unless remote access is deliberately
  protected by separate network controls.
- Remove the CA from every browser and system trust store when finished.
- Deleting `ca.crt` from disk does not remove previously installed trust.
- If `ca.key` may have leaked, remove its certificate from every trust store,
  delete both local CA files, and restart the proxy to generate a new pair.
