# Wrapper around docker compose for the Fabric integration harness.
# Explicit targets keep the common flows discoverable; pass extra
# args through the `ARGS=` variable, e.g. `make logs ARGS="-f otel-collector"`.

COMPOSE ?= docker compose
PROJECT ?= fabric-harness

.PHONY: up up-bootstrap down reset logs ps build curl-health smoke

up: ## Start core harness (sidecars + collector + Langfuse).
	mkdir -p run
	@if [ ! -s run/presidio.key ]; then \
		openssl rand -base64 32 > run/presidio.key; \
	fi
	$(COMPOSE) up -d --build
	@echo ""
	@echo "Langfuse UI:  http://localhost:$${FABRIC_LANGFUSE_PORT:-3000}"
	@echo "OTLP/gRPC:    localhost:$${FABRIC_OTLP_GRPC_PORT:-4317}"
	@echo "OTLP/HTTP:    localhost:$${FABRIC_OTLP_HTTP_PORT:-4318}"
	@echo ""
	@echo "Guardrail sockets live on the 'fabric-run' named volume, not on"
	@echo "the host. Run your agent as a container to reach them:"
	@echo "  docker run --rm --network $(PROJECT)_fabric \\"
	@echo "    -v $(PROJECT)_fabric-run:/run/fabric --user 1000:1000 \\"
	@echo "    -e OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318 <image>"
	@echo "  UDS paths inside the container: /run/fabric/{presidio,nemo}.sock"

up-bootstrap: up ## Start harness AND run the Langfuse bootstrap Job.
	$(COMPOSE) --profile bootstrap run --rm langfuse-bootstrap

down: ## Stop everything; keep Postgres volume.
	$(COMPOSE) down

reset: ## Stop + delete volumes (Langfuse DB, Fabric UDS dir).
	$(COMPOSE) down -v
	rm -rf run

logs: ## Tail logs; pass ARGS="<service>" to filter.
	$(COMPOSE) logs -f $(ARGS)

ps: ## Show running services.
	$(COMPOSE) ps

build: ## Build all local images without starting.
	$(COMPOSE) build

curl-health: ## Hit each service's healthcheck endpoint.
	@curl -fsS http://localhost:3000/api/public/health && echo "  <- langfuse OK"
	@curl -fsS http://localhost:4318/v1/traces -X POST -H 'Content-Type: application/json' -d '{}' \
		&& echo "  <- otel-collector OK" \
		|| echo "  <- otel-collector reachable (empty body is expected)"

smoke: ## Run the Fabric SDK smoke test from the host against the harness.
	cd ../../examples/harness-smoke && python smoke.py

help:
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
