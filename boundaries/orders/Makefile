GO ?= go
GOLANGCI_LINT ?= golangci-lint
DOCKER ?= docker
TEMPORAL_IMAGE ?= temporalio/temporal:1.9.1
TEMPORAL_PORT ?= 7233
TEMPORAL_CONTAINER ?= orders-temporal-dev

.PHONY: check run run-worker run-consumer generate proto-check proto-generate test test-integration test-integration-kafka temporal-dev temporal-dev-stop lint format format-check

check: proto-check test lint format-check

run:
	$(GO) run ./cmd/orders

# Worker против локального Temporal (make temporal-dev): регистрирует Search
# Attribute и симулирует соседние контексты.
run-worker:
	MANAGEMENT__PORT=9082 TEMPORAL__REGISTER_SEARCH_ATTRIBUTES=true FULFILLMENT__NEIGHBOURS=simulated \
		$(GO) run ./cmd/orders-worker

# Consumer против локальных Kafka и Temporal (make temporal-dev).
run-consumer:
	MANAGEMENT__PORT=9083 $(GO) run ./cmd/orders-consumer

temporal-dev:
	$(DOCKER) run -d --rm --name $(TEMPORAL_CONTAINER) -p $(TEMPORAL_PORT):7233 -p 8233:8233 \
		$(TEMPORAL_IMAGE) server start-dev --ip 0.0.0.0
	@for i in $$(seq 1 60); do \
		$(DOCKER) exec $(TEMPORAL_CONTAINER) temporal operator namespace describe --namespace default >/dev/null 2>&1 && exit 0; \
		sleep 1; \
	done; echo "temporal dev server did not become ready" >&2; exit 1

temporal-dev-stop:
	-$(DOCKER) rm -f $(TEMPORAL_CONTAINER)

generate:
	$(MAKE) proto-generate
	$(GO) tool wire ./di

proto-check:
	cd ../.. && buf format --diff --exit-code
	cd ../.. && buf lint
	cd ../.. && buf build

proto-generate:
	cd infrastructure/grpc && buf generate --template buf.gen.yaml
	cd slices/order/infrastructure/neighbours/grpcclient && buf generate --template buf.gen.yaml
	cd slices/order/infrastructure/messaging && buf generate --template buf.gen.yaml

test:
	$(GO) test ./...

# AC-446: worker end-to-end против Temporal dev server в Docker.
test-integration:
	$(MAKE) temporal-dev
	TEMPORAL_ADDRESS=localhost:$(TEMPORAL_PORT) $(GO) test -tags integration -count=1 -run Integration\|AgainstTemporal ./di/...; \
		status=$$?; $(MAKE) temporal-dev-stop; exit $$status

# ADR 28: Kafka → Watermill → EventGroupProcessor → Inbox → handler против Kafka
# в Testcontainers (нужен Docker).
test-integration-kafka:
	$(GO) test -tags integration -count=1 -run AgainstKafka ./slices/order/infrastructure/messaging/...

lint:
	$(GOLANGCI_LINT) run

format:
	$(GO) fmt ./...

format-check:
	$(GOLANGCI_LINT) fmt --diff
