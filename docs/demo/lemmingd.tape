# Record with: make demo   (requires https://github.com/charmbracelet/vhs)
Output docs/demo/lemmingd.gif

Set Shell bash
Set FontSize 16
Set Width 1200
Set Height 560
Set TypingSpeed 35ms
Set Theme "Catppuccin Mocha"

Hide
Type `export D=$(mktemp -d)`
Enter
Sleep 500ms
Type `python3 -m http.server 19900 --bind 127.0.0.1 --directory docs/demo/site >/dev/null 2>&1 &`
Enter
Sleep 2s
Type `printf '{"version":1,"listen":"127.0.0.1:18082","rules":[{"target":"127.0.0.1:19900","domains":["example.com"],"include_paths":["/"]}]}' > "$D/config.json" && chmod 600 "$D/config.json"`
Enter
Sleep 500ms
Type `./bin/lemmingd -config "$D/config.json" -cacert "$D/ca.crt" -cakey "$D/ca.key" 2>"$D/log" &`
Enter
Sleep 2s
Type "clear"
Enter
Sleep 1s
Show

Type "# The real site, fetched directly" Enter
Type "curl -s https://example.com/ | grep -o '<title>.*</title>'" Enter
Sleep 3s
Type "# The same HTTPS URL through lemmingd: / now comes from a local server" Enter
Type `curl -s --proxy http://127.0.0.1:18082 --cacert "$D/ca.crt" https://example.com/ | grep -o '<title>.*</title>'`
Enter
Sleep 3s
Type "# Browsers get the same view: lemmingd firefox https://example.com/" Enter
Sleep 4s

Hide
Type `kill %1 %2; rm -rf "$D"`
Enter
