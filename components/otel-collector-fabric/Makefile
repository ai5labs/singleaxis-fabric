# SingleAxis Fabric OTel Collector distribution — build helpers.
#
# Prereqs: Go >= 1.25, and the OpenTelemetry Collector Builder:
#   go install go.opentelemetry.io/collector/cmd/builder@v0.150.0
#
# The `builder` binary is traditionally named `ocb` by operators; both
# names work. This Makefile uses whichever is on $PATH.

OCB ?= $(shell command -v ocb || command -v builder)
DIST_DIR := dist
PROCESSOR_DIRS := processor/fabricguardprocessor processor/fabricpolicyprocessor processor/fabricsamplerprocessor processor/fabricredactprocessor

.PHONY: all build test lint clean tidy

all: test build

build:
	@if [ -z "$(OCB)" ]; then \
		echo "ocb/builder not found. Install with:"; \
		echo "  go install go.opentelemetry.io/collector/cmd/builder@v0.150.0"; \
		exit 1; \
	fi
	$(OCB) --config ocb-config.yaml

test:
	@for d in $(PROCESSOR_DIRS); do \
		echo "==> test $$d"; \
		(cd $$d && go test -race -cover ./...) || exit 1; \
	done

tidy:
	@for d in $(PROCESSOR_DIRS); do (cd $$d && go mod tidy) || exit 1; done

lint:
	@for d in $(PROCESSOR_DIRS); do (cd $$d && go vet ./...) || exit 1; done

clean:
	rm -rf $(DIST_DIR)
