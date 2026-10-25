UV ?= uv

.PHONY: check run run-relay generate proto-check test lint imports typecheck format format-check lint-fix

check: proto-check test lint imports typecheck format-check

generate:
	$(MAKE) -C ../.. buf-generate

proto-check:
	$(MAKE) -C ../.. buf-check

run:
	$(UV) run python -m entrypoints.payments.main

run-relay:
	$(UV) run python -m entrypoints.payments_relay.main

test:
	$(UV) run pytest

lint:
	$(UV) run ruff check .

# Architecture boundaries: [tool.importlinter] in pyproject.toml.
imports:
	$(UV) run lint-imports

typecheck:
	$(UV) run ty check

format:
	$(UV) run ruff format .

format-check:
	$(UV) run ruff format --check .

lint-fix:
	$(UV) run ruff check --fix .
