CARGO ?= cargo

.PHONY: check run generate generate-spicedb proto-check test test-spicedb lint format format-check

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

lint:
	$(CARGO) clippy --all-targets --all-features

format:
	$(CARGO) fmt

format-check:
	$(CARGO) fmt --check
