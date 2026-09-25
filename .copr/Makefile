.PHONY: srpm

RPKG ?= rpkg

# Generate the module archive during COPR's network-enabled SRPM stage; the
# resulting Source1 keeps all architecture buildroots offline.
srpm:
	$(RPKG) srpm --spec "$(spec)" --outdir "$(outdir)"
