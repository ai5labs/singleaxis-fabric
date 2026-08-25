.PHONY: build test validate-example

OCB ?= builder

build:
	$(OCB) --config ocb-config.yaml

validate-example:
	@test -x ./dist/fabric-relay || (echo "build ./dist/fabric-relay first" >&2; exit 1)
	FABRIC_RELAY_AUTH_HEADER=test ./dist/fabric-relay validate --config examples/production.yaml

test:
	bash tests/test-config.sh
