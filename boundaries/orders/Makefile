GO ?= go
GOLANGCI_LINT ?= golangci-lint
DOCKER ?= docker
POSTGRES_IMAGE ?= postgres:18.6-alpine3.24
POSTGRES_PORT ?= 5433
POSTGRES_CONTAINER ?= orders-postgres-dev
POSTGRES_DEV_DSN ?= postgres://orders:orders@localhost:$(POSTGRES_PORT)/orders?sslmode=disable

.PHONY: check run postgres-dev postgres-dev-stop test-postgres generate proto-check proto-generate test lint format format-check

check: proto-check test lint format-check

# API требует SpiceDB (ADR 18): по умолчанию — локальный из `make spicedb-dev` в корне.
run:
	SPICEDB__ENDPOINT=$${SPICEDB__ENDPOINT:-localhost:50051} SPICEDB__PRESHARED_KEY=$${SPICEDB__PRESHARED_KEY:-devkey} \
		SPICEDB__INSECURE=$${SPICEDB__INSECURE:-true} POSTGRES__DSN=$${POSTGRES__DSN:-$(POSTGRES_DEV_DSN)} \
		$(GO) run ./cmd/orders

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
	cd pkg/gateway && buf generate --template buf.gen.yaml
	cd slices/order/infrastructure/messaging && buf generate --template buf.gen.yaml
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
