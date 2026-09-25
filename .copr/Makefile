.PHONY: srpm

RPKG ?= rpkg
DNF ?= dnf

# Generate the module archive during COPR's network-enabled SRPM stage; the
# resulting Source1 keeps all architecture buildroots offline.
srpm:
	$(DNF) install -y golang rpkg
	$(RPKG) srpm --spec "$(spec)" --outdir "$(outdir)"
