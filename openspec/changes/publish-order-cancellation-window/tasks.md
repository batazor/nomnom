# Tasks

## 1. Домен и политика

- [ ] 1.1 Добавить в `domains/order` чистую проверку «Order State допускает Cancellation» поверх transition table и использовать её в `CheckCancellation`; проверить табличным тестом для всех состояний (`FR-115`).
- [ ] 1.2 Добавить адаптер `slices/order/infrastructure/cancellationpolicy` с окном `[placedAt, placedAt + window]`; проверить unit-тестом границы окна.
- [ ] 1.3 Добавить секцию `cancellation.window` в `pkg/config` (default `5m`, env `CANCELLATION__WINDOW`, валидация `> 0`); проверить тестами конфигурации default, override и отказ для `0s` и мусора (`FR-462`, `AC-506`).

## 2. Get Order

- [ ] 2.1 Добавить в `getorder` порт cancellation policy и поле окна в `OrderView`; окно запрашивать только для состояний, допускающих Cancellation; проверить тестами handler `placed`, `confirmed`, `cancelled`, `completed` (`AC-504`, `AC-505`).
- [ ] 2.2 При ошибке политики возвращать `OrderView` без окна, писать ошибку в log и span; проверить тестом handler с отказывающей политикой (`FR-463`, `AC-507`).
- [ ] 2.3 Подключить один экземпляр политики из конфигурации в `slices/order/di`; проверить, что production composition root не использует `memory.AlwaysOpen` и `memory.FixedWindow` (`FR-462`).

## 3. gRPC

- [ ] 3.1 Добавить `CancellationWindow` и `Order.cancellation_window = 11` в `orders.proto`; выполнить `make proto-check`, `make proto-generate` и `buf breaking` против `main`.
- [ ] 3.2 Перевести окно в `orderView` gRPC-сервиса; проверить тестом сервиса наличие окна для `placed` и отсутствие для `completed` (`AC-504`, `AC-505`).

## 4. Документация и проверка

- [ ] 4.1 Обновить README `orders` (Get Order, cancellation policy, placeholder), `config.example.yaml`, Helm values и раздел Customer API в README BFF (правило `availableUntil = closes_at` при `opens_at ≤ now ≤ closes_at`); проверить Markdown links.
- [ ] 4.2 Выполнить `make test`, `make lint` и `make proto-check` в `boundaries/orders`, `openspec validate publish-order-cancellation-window --strict` и `git diff --check`.

## Workflow follow-up

- Опубликовать Request Order Cancellation в gRPC с той же политикой.
- Реализовать resolver `Order.cancellation` в BFF.
- После review реализации архивировать change через OpenSpec archive workflow.
