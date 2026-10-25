UV ?= uv

.PHONY: check run run-relay generate proto-check test lint typecheck format format-check lint-fix

check: proto-check test lint typecheck format-check

generate:
	$(MAKE) -C ../.. buf-generate

proto-check:
	$(MAKE) -C ../.. buf-check

run:
	$(UV) run python -m entrypoints.payments.main

run-relay:
	$(UV) run python -m entrypoints.payments_relay.main

test:
	$(UV) run python -m unittest discover -s tests

lint:
	$(UV) run ruff check .

typecheck:
	$(UV) run ty check

format:
	$(UV) run ruff format .

format-check:
	$(UV) run ruff format --check .

lint-fix:
	$(UV) run ruff check --fix .
