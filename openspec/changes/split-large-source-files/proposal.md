# Proposal

## Why

Часть файлов выросла до сотен строк с несколькими ответственностями: в `delivery` `infrastructure/grpc/identity.rs` — 1182 строки, `pkg/config/mod.rs` — 1006, `infrastructure/postgres/mod.rs` — 959, `infrastructure/reconcile.rs` — 814; в `restaurants` `pkg/config/config.ts` — 555, `restaurantorder.ts` — 550; в `payments` `infrastructure/messaging/relay.py` — 501, `pkg/config/__init__.py` — 497; в `orders` `memory/store.go`, `domains/order/order.go`, `memory/adapters.go`, `postgres/queries.go` — 320–370. Такие файлы труднее читать и ревьюить, а в Rust существенная часть объёма — встроенные `mod tests`. В Go пакет естественно делится на файлы без изменения API, но сейчас многие пакеты держат всё в одном-двух файлах. Ограничения нигде не записаны и не проверяются, поэтому файлы продолжают расти.

## What Changes

- Правило размера исходных файлов: production-код не больше 300 строк, тесты не больше 500 строк; сгенерированный код (Protobuf, Wire, codegen GraphQL, `dist`) исключён. Исключение допускается только записью в allowlist с причиной.
- Проверка `ops/checks/file-size.sh` и цель `make check-file-size` в корневом `Makefile`; allowlist `ops/checks/file-size-allowlist.txt` в формате `<path> <reason>`. Проверка запускается вручную и в CI on demand (ADR 30).
- Разбиение файлов-нарушителей во всех приложениях (`orders`, `payments`, `restaurants`, `delivery`, `bff`, `ui`) по ответственности, без изменения поведения и публичных API:
  - **Go**: пакет делится на файлы по теме (типы и конструкторы, отдельные операции, ошибки, адаптеры), тесты — по тем же темам;
  - **Rust**: встроенный `#[cfg(test)] mod tests` выносится в `<module>/tests.rs` или `tests/`, крупный `mod.rs` — в подмодули;
  - **TypeScript, Python**: модуль делится на модули по ответственности с прежними экспортами из исходного модуля, если на него ссылаются извне.
- Файлы, которые меняют параллельные changes `extract-order-fulfillment-slice`, `fix-delivery-slice-boundaries` и `rename-restaurant-order-slice`, разбиваются после них, в том же исполнении по контексту.

Не входит: изменение поведения, переименование публичных API, рефакторинг логики сверх разбиения.

## Capabilities

### New Capabilities

- `source-file-size`: ограничение размера исходных файлов и его проверка.

### Modified Capabilities

Нет.

## Impact

- Все приложения в `boundaries/`, корневой `Makefile`, `ops/checks/`.
- Сгенерированный код и внешние контракты не меняются.
