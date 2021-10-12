package main

const j2Index = `
#!/bin/bash +x -e -u
source /etc/os-release
if [ "${ID}" = "fedora" ]; then
	curl -s {{ .ReqHost }}{{ .CaURL }} -o /etc/pki/ca-trust/source/anchors/proxy.dev.crt
	update-ca-trust
	dnf config-manager \
	  --setopt=proxy={{ .ReqHost }} \
	  --setopt=best=False \
	  --setopt=installonly_limit=3 \
	  --setopt=clean_requirements_on_remove=True \
	  --setopt=install_weak_deps=False \
	  --setopt=fastestmirror=False \
	  --save
	
	dnf config-manager \
	  --setopt          'fedora.baseurl=https://d2lzkl7pfhq30w.cloudfront.net/pub/fedora/linux/releases/$releasever/Everything/$basearch/os/' \
	  --setopt  'fedora-modular.baseurl=https://d2lzkl7pfhq30w.cloudfront.net/pub/fedora/linux/releases/$releasever/Modular/$basearch/os/'    \
	  --setopt         'updates.baseurl=https://d2lzkl7pfhq30w.cloudfront.net/pub/fedora/linux/updates/$releasever/Everything/$basearch/'     \
	  --setopt 'updates-modular.baseurl=https://d2lzkl7pfhq30w.cloudfront.net/pub/fedora/linux/updates/$releasever/Modular/$basearch/'        \
	  --setopt          fedora.metalink=None \
	  --setopt  fedora-modular.metalink=None \
	  --setopt         updates.metalink=None \
	  --setopt updates-modular.metalink=None \
	  --setopt fedora-cisco-openh264.enabled=0 \
	  --save
	
	rpm -q rpmfusion-free-release rpmfusion-nonfree-release || dnf install -y \
	  https://mirrors.rpmfusion.org/free/fedora/rpmfusion-free-release-${VERSION_ID}.noarch.rpm \
	  https://mirrors.rpmfusion.org/nonfree/fedora/rpmfusion-nonfree-release-${VERSION_ID}.noarch.rpm
	sed -i 's/^metalink=/#\0/; s/#baseurl=/baseurl=/' /etc/yum.repos.d/rpmfusion*repo
fi
echo Done
`

type paramsJ2Index struct {
	ReqHost string
	CaURL   string
}
