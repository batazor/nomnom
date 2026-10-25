CARGO ?= cargo

.PHONY: check run generate proto-check test lint format format-check

check: proto-check test lint format-check

generate:
	$(MAKE) -C ../.. buf-generate

proto-check:
	$(MAKE) -C ../.. buf-check

run:
	$(CARGO) run

test:
	$(CARGO) test

lint:
	$(CARGO) clippy --all-targets --all-features

format:
	$(CARGO) fmt

format-check:
	$(CARGO) fmt --check
