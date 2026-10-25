CARGO ?= cargo

.PHONY: check run generate generate-spicedb proto-check test test-spicedb lint format format-check sqlx-prepare sqlx-prepare-check

check: proto-check test lint format-check

generate: generate-spicedb
	$(MAKE) -C ../.. buf-generate

# SpiceDB client stubs (authzed.api.v1) из buf.build/authzed/api.
generate-spicedb:
	cd infrastructure/spicedb && buf generate --template buf.gen.yaml

proto-check:
	$(MAKE) -C ../.. buf-check

run:
	$(CARGO) run

test:
	$(CARGO) test

# Integration-тесты против SpiceDB из testcontainers с authorization/bootstrap.yaml.
test-spicedb:
	$(CARGO) test --test spicedb_authorization --test spicedb_relationships

# Offline query cache .sqlx compile-time checked sqlx macros: одноразовый PostgreSQL
# в docker, миграции infrastructure/postgres/migrations, cargo sqlx prepare
# (sqlx-cli той же версии, что sqlx в Cargo.lock), остановка контейнера.
# sqlx-prepare-check сверяет закоммиченный кеш с запросами, не переписывая его.
SQLX_PG_IMAGE ?= postgres:18.6-alpine3.24
SQLX_PG_CONTAINER ?= delivery-sqlx-prepare
SQLX_PG_PORT ?= 55432
SQLX_DATABASE_URL = postgres://postgres:postgres@127.0.0.1:$(SQLX_PG_PORT)/delivery

sqlx-prepare: SQLX_PREPARE_FLAGS =
sqlx-prepare-check: SQLX_PREPARE_FLAGS = --check
sqlx-prepare sqlx-prepare-check:
	docker run -d --rm --name $(SQLX_PG_CONTAINER) -e POSTGRES_PASSWORD=postgres \
		-e POSTGRES_DB=delivery -p 127.0.0.1:$(SQLX_PG_PORT):5432 $(SQLX_PG_IMAGE) >/dev/null
	trap 'docker stop $(SQLX_PG_CONTAINER) >/dev/null' EXIT; \
	until docker exec $(SQLX_PG_CONTAINER) pg_isready -h 127.0.0.1 -U postgres -d delivery >/dev/null; do sleep 1; done; \
	export DATABASE_URL=$(SQLX_DATABASE_URL) SQLX_OFFLINE=false; \
	$(CARGO) sqlx migrate run --source infrastructure/postgres/migrations && \
	$(CARGO) sqlx prepare $(SQLX_PREPARE_FLAGS) -- --all-targets

lint:
	$(CARGO) clippy --all-targets --all-features

format:
	$(CARGO) fmt

format-check:
	$(CARGO) fmt --check
