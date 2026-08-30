COMPOSE ?= docker compose

.PHONY: up down reset logs status build smoke qualify help

up: ## Build and start Fabric Node plus the controlled sink.
	$(COMPOSE) up -d --build --wait
	@echo "Fabric Node OTLP/HTTP: http://localhost:$${FABRIC_OTLP_HTTP_PORT:-4318}"
	@echo "Controlled sink status: http://localhost:$${FABRIC_SINK_PORT:-8080}/count"

down: ## Stop the evaluation setup while keeping durable volumes.
	$(COMPOSE) down

reset: ## Delete this evaluation setup and its queue/sink volumes.
	$(COMPOSE) down -v

logs: ## Tail logs; use ARGS="fabric-node" to select a service.
	$(COMPOSE) logs -f $(ARGS)

status: ## Show containers and the controlled sink's accepted request count.
	$(COMPOSE) ps
	@curl -fsS "http://$$($(COMPOSE) port test-sink 8080)/count"; echo

build: ## Build the local Fabric Node image.
	$(COMPOSE) build fabric-node

smoke: ## Send one protected OTLP trace to Fabric Node.
	@curl -fsS -X POST "http://$$($(COMPOSE) port fabric-node 4318)/v1/traces" \
		-H 'Content-Type: application/json' \
		--data-binary @fixtures/decision-summary.json >/dev/null
	@echo "trace accepted by Fabric Node"

qualify: up ## Exercise protection plus destination outage/restart recovery.
	./qualify.sh

help:
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
