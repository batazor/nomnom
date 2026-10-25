# Tasks

## 1. Разбиение по контекстам

- [ ] 1.1 `orders` (после `extract-order-fulfillment-slice`): пакеты Go делятся на файлы по теме, тесты — так же; `go test ./...`, `go vet ./...`, `make lint` (`FR-489`, `AC-554`).
- [ ] 1.2 `delivery` (после `fix-delivery-slice-boundaries`): крупные встроенные тесты в `tests.rs`, `mod.rs` с несколькими компонентами — в подмодули с `pub use`; `cargo test`, `cargo clippy --all-targets` (`FR-489`).
- [ ] 1.3 `restaurants` (после `rename-restaurant-order-slice`): модули и тесты по ответственности; агрегаты не дробятся без причины (`AC-553`); `npm test`, typecheck, `npm run check` (`FR-489`).
- [x] 1.4 `payments`: модули и тесты по ответственности; тесты, `ruff`, `ty` (`FR-489`).
- [x] 1.5 `bff` и `ui`: файлы со смешанными ответственностями, если есть; проверки приложений (`FR-489`).

## 2. Завершение

- [ ] 2.1 `openspec validate split-large-source-files --strict`, `git diff --check`.
