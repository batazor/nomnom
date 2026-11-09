GO ?= go
GOLANGCI_LINT ?= golangci-lint
DOCKER ?= docker
POSTGRES_IMAGE ?= postgres:18.6-alpine3.24
POSTGRES_PORT ?= 5433
POSTGRES_CONTAINER ?= orders-postgres-dev
POSTGRES_DEV_DSN ?= postgres://orders:orders@localhost:$(POSTGRES_PORT)/orders?sslmode=disable

.PHONY: check run postgres-dev postgres-dev-stop test-postgres generate proto-check proto-generate test lint format format-check

check: proto-check test lint format-check

# Локальный API: несекретная конфигурация — файл CONFIG_FILE (config.example.yaml: SpiceDB из
# `make spicedb-dev` в корне, ADR 18), секреты и имя pod — env. PostgreSQL — `make postgres-dev`.
CONFIG_FILE ?= config.example.yaml
POD_NAME ?= orders-local
SPICEDB_DEV_PRESHARED_KEY ?= devkey

run:
	CONFIG_FILE=$(CONFIG_FILE) POD_NAME=$(POD_NAME) SPICEDB__PRESHARED_KEY=$(SPICEDB_DEV_PRESHARED_KEY) \
		POSTGRES__DSN=$(POSTGRES_DEV_DSN) $(GO) run ./cmd/orders

postgres-dev:
	$(DOCKER) run -d --rm --name $(POSTGRES_CONTAINER) -p $(POSTGRES_PORT):5432 \
		-e POSTGRES_USER=orders -e POSTGRES_PASSWORD=orders -e POSTGRES_DB=orders $(POSTGRES_IMAGE)
	@for i in $$(seq 1 60); do \
		$(DOCKER) exec $(POSTGRES_CONTAINER) pg_isready -U orders -d orders >/dev/null 2>&1 && exit 0; \
		sleep 1; \
	done; echo "postgres did not become ready" >&2; exit 1

postgres-dev-stop:
	-$(DOCKER) rm -f $(POSTGRES_CONTAINER)

# Контракт PostgreSQL-адаптеров; PostgreSQL поднимается testcontainers в TestMain.
test-postgres:
	$(GO) test -count=1 ./slices/order/infrastructure/repository/...

generate:
	$(MAKE) proto-generate
	$(GO) tool wire ./di

proto-check:
	cd ../.. && buf format --diff --exit-code
	cd ../.. && buf lint
	cd ../.. && buf build

proto-generate:
	cd infrastructure/grpc && buf generate --template buf.gen.yaml
	$(GO) tool mockery

# Все тесты, включая PostgreSQL, Kafka, SpiceDB и Temporal в testcontainers (нужен Docker).
test:
	$(GO) test ./...

lint:
	$(GOLANGCI_LINT) run

format:
	$(GO) fmt ./...

format-check:
	$(GOLANGCI_LINT) fmt --diff
