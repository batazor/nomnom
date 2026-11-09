UV ?= uv

.PHONY: check run generate proto-check test lint imports typecheck format format-check lint-fix

check: proto-check test lint imports typecheck format-check

generate:
	$(MAKE) -C ../.. buf-generate

proto-check:
	$(MAKE) -C ../.. buf-check

# Несекретная конфигурация — config.example.yaml, секреты — .env (по образцу .env.example).
run:
	CONFIG_FILE=config.example.yaml POD_NAME=payments-local $(UV) run --env-file .env python -m entrypoints.payments.main

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
