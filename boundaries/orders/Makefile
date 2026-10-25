GO ?= go
GOLANGCI_LINT ?= golangci-lint
DOCKER ?= docker
TEMPORAL_IMAGE ?= temporalio/temporal:1.9.1
TEMPORAL_PORT ?= 7233
TEMPORAL_CONTAINER ?= orders-temporal-dev
POSTGRES_IMAGE ?= postgres:18.6-alpine3.24
POSTGRES_PORT ?= 5433
POSTGRES_CONTAINER ?= orders-postgres-dev
POSTGRES_DEV_DSN ?= postgres://orders:orders@localhost:$(POSTGRES_PORT)/orders?sslmode=disable

.PHONY: check run run-worker migrate postgres-dev postgres-dev-stop test-postgres run-consumer generate proto-check proto-generate test temporal-dev temporal-dev-stop lint format format-check

check: proto-check test lint format-check

# API требует SpiceDB (ADR 18): по умолчанию — локальный из `make spicedb-dev` в корне.
run:
	SPICEDB__ENDPOINT=$${SPICEDB__ENDPOINT:-localhost:50051} SPICEDB__PRESHARED_KEY=$${SPICEDB__PRESHARED_KEY:-devkey} \
		SPICEDB__INSECURE=$${SPICEDB__INSECURE:-true} POSTGRES__DSN=$${POSTGRES__DSN:-$(POSTGRES_DEV_DSN)} \
		$(GO) run ./cmd/orders

# Worker против локального Temporal (make temporal-dev): регистрирует Search
# Attribute и симулирует соседние контексты.
run-worker:
	MANAGEMENT__PORT=9082 TEMPORAL__REGISTER_SEARCH_ATTRIBUTES=true FULFILLMENT__NEIGHBOURS=simulated \
		POSTGRES__DSN=$${POSTGRES__DSN:-$(POSTGRES_DEV_DSN)} \
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

postgres-dev:
	$(DOCKER) run -d --rm --name $(POSTGRES_CONTAINER) -p $(POSTGRES_PORT):5432 \
		-e POSTGRES_USER=orders -e POSTGRES_PASSWORD=orders -e POSTGRES_DB=orders $(POSTGRES_IMAGE)
	@for i in $$(seq 1 60); do \
		$(DOCKER) exec $(POSTGRES_CONTAINER) pg_isready -U orders -d orders >/dev/null 2>&1 && exit 0; \
		sleep 1; \
	done; echo "postgres did not become ready" >&2; exit 1

postgres-dev-stop:
	-$(DOCKER) rm -f $(POSTGRES_CONTAINER)

migrate:
	POSTGRES__DSN=$${POSTGRES__DSN:-$(POSTGRES_DEV_DSN)} $(GO) run ./cmd/orders-migrate

# Контракт PostgreSQL-адаптеров; PostgreSQL поднимается testcontainers в TestMain.
test-postgres:
	$(GO) test -count=1 ./slices/order/infrastructure/postgres/...

generate:
	$(MAKE) proto-generate
	$(GO) tool wire ./di

proto-check:
	cd ../.. && buf format --diff --exit-code
	cd ../.. && buf lint
	cd ../.. && buf build

proto-generate:
	cd infrastructure/grpc && buf generate --template buf.gen.yaml
	cd pkg/gateway && buf generate --template buf.gen.yaml
	cd slices/order/infrastructure/messaging && buf generate --template buf.gen.yaml

# Все тесты, включая PostgreSQL, Kafka, SpiceDB и Temporal в testcontainers (нужен Docker).
test:
	$(GO) test ./...

lint:
	$(GOLANGCI_LINT) run

format:
	$(GO) fmt ./...

format-check:
	$(GOLANGCI_LINT) fmt --diff
