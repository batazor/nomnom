# Tasks

## 1. Проверка

- [x] 1.1 `ops/checks/file-size.sh`, `ops/checks/file-size-allowlist.txt`, цель `check-file-size` в корневом `Makefile`; исключения по design (решение 2); проверить на временном файле на 320 строк (`FR-488`, `AC-553`).

## 2. Разбиение по контекстам

- [ ] 2.1 `orders` (после `extract-order-fulfillment-slice`): пакеты Go делятся на файлы по теме, тесты — так же; `go test ./...`, `go vet ./...`, `make lint` (`FR-489`, `AC-554`).
- [ ] 2.2 `delivery` (после `fix-delivery-slice-boundaries`): встроенные тесты в `tests.rs`, крупные `mod.rs` в подмодули с `pub use`; `cargo test`, `cargo clippy --all-targets` (`FR-489`).
- [ ] 2.3 `restaurants` (после `rename-restaurant-order-slice`): модули и тесты по ответственности; `npm test`, typecheck, `npm run check` (`FR-489`).
- [x] 2.4 `payments`: модули и тесты по ответственности; тесты, `ruff`, `ty` (`FR-489`).
- [x] 2.5 `bff` и `ui`: нарушители, если есть; проверки приложений (`FR-489`).

## 3. Завершение

- [ ] 3.1 `make check-file-size` проходит; allowlist содержит только неделимые файлы с причиной; описать правило в `boundaries/README.md`.
- [ ] 3.2 `openspec validate split-large-source-files --strict`, `git diff --check`.
