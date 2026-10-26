GO ?= go
GOLANGCI_LINT ?= golangci-lint
DOCKER ?= docker
POSTGRES_IMAGE ?= postgres:18.6-alpine3.24
POSTGRES_PORT ?= 5436
POSTGRES_CONTAINER ?= controlplane-postgres-dev
POSTGRES_DEV_DSN ?= postgres://controlplane:controlplane@localhost:$(POSTGRES_PORT)/controlplane?sslmode=disable

.PHONY: check run postgres-dev postgres-dev-stop generate proto-check proto-generate test test-postgres lint format format-check

check: proto-check test lint format-check

# API требует SpiceDB (ADR 18, `make spicedb-dev` в корне), PostgreSQL (`make postgres-dev`),
# identity (ADR 27, ADR 32) и маршруты (ROUTES__*, ADR 41): IDENTITY__* и ROUTES__* задаёт
# окружение или CONFIG_FILE (config.example.yaml). Маршруты применяются через in-cluster config —
# процесс стартует только в pod кластера.
run:
	SPICEDB__ENDPOINT=$${SPICEDB__ENDPOINT:-localhost:50051} SPICEDB__PRESHARED_KEY=$${SPICEDB__PRESHARED_KEY:-devkey} \
		SPICEDB__INSECURE=$${SPICEDB__INSECURE:-true} POSTGRES__DSN=$${POSTGRES__DSN:-$(POSTGRES_DEV_DSN)} \
		$(GO) run ./cmd/controlplane

postgres-dev:
	$(DOCKER) run -d --rm --name $(POSTGRES_CONTAINER) -p $(POSTGRES_PORT):5432 \
		-e POSTGRES_USER=controlplane -e POSTGRES_PASSWORD=controlplane -e POSTGRES_DB=controlplane $(POSTGRES_IMAGE)
	@for i in $$(seq 1 60); do \
		$(DOCKER) exec $(POSTGRES_CONTAINER) pg_isready -U controlplane -d controlplane >/dev/null 2>&1 && exit 0; \
		sleep 1; \
	done; echo "postgres did not become ready" >&2; exit 1

postgres-dev-stop:
	-$(DOCKER) rm -f $(POSTGRES_CONTAINER)

generate:
	$(MAKE) proto-generate
	$(GO) tool wire ./di

proto-check:
	cd ../.. && buf format --diff --exit-code
	cd ../.. && buf lint
	cd ../.. && buf build

proto-generate:
	cd infrastructure/grpc && buf generate --template buf.gen.yaml

# Контракт PostgreSQL-адаптеров; PostgreSQL поднимается testcontainers в TestMain.
test-postgres:
	$(GO) test -count=1 ./slices/topology/infrastructure/repository/...

# Все тесты, включая PostgreSQL и SpiceDB в testcontainers (нужен Docker).
test:
	$(GO) test ./...

lint:
	$(GOLANGCI_LINT) run

format:
	$(GO) fmt ./...

format-check:
	$(GOLANGCI_LINT) fmt --diff
