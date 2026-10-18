# Record with: make demo   (requires https://github.com/charmbracelet/vhs)
Output docs/demo/hamsterd.gif

Set Shell bash
Set FontSize 16
Set Width 1200
Set Height 560
Set TypingSpeed 35ms
Set Theme "Catppuccin Mocha"

Hide
Type `export D=$(mktemp -d) U=https://releases.hashicorp.com/terraform-provider-aws/5.70.0/terraform-provider-aws_5.70.0_linux_amd64.zip`
Enter
Sleep 500ms
Type `printf '{"version":1,"listen":"127.0.0.1:18081","cache":{"directory":"%s/cache","max_size_mib":512,"max_object_mib":256,"default_ttl":"1h"}}' "$D" > "$D/config.json" && chmod 600 "$D/config.json"`
Enter
Sleep 500ms
Type `./bin/hamsterd -config "$D/config.json" -cacert "$D/ca.crt" -cakey "$D/ca.key" 2>"$D/log" &`
Enter
Sleep 2s
Type "clear"
Enter
Sleep 1s
Show

Type "# Download the Terraform AWS provider (139 MB) through hamsterd: a cache MISS" Enter
Type `time curl -s --proxy http://127.0.0.1:18081 --cacert "$D/ca.crt" -o /dev/null -D - "$U" | grep -i x-hamsterd-cache`
Enter
Sleep 10s
Type "# The same download again, from any build, container, or team machine" Enter
Type `time curl -s --proxy http://127.0.0.1:18081 --cacert "$D/ca.crt" -o /dev/null -D - "$U" | grep -i x-hamsterd-cache`
Enter
Sleep 4s

Hide
Type `kill %1; rm -rf "$D"`
Enter
