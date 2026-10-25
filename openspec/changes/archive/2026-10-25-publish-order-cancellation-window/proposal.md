# Proposal

## Why

Customer GraphQL contract (`add-customer-graphql-contract`) показывает действие отмены по `Order.cancellation.availableUntil`, но BFF не может заполнить это поле: `GetOrder` в `orders` не возвращает окно отмены. Окно существует только как порт `CancellationPolicy` use case Request Order Cancellation, и в production он не подключён: есть лишь тестовые адаптеры `AlwaysOpen` и `FixedWindow` в `infrastructure/memory`. Без опубликованного окна customer UI не предлагает отмену вовсе, а когда окно появится, его нельзя будет вычислять в BFF или UI: это правило `orders`.

## What Changes

- Добавить production-адаптер `CancellationPolicy` с окном из конфигурации: окно открывается в момент Place Order и длится `cancellation.window` (`CANCELLATION__WINDOW`, по умолчанию `5m` как placeholder до продуктового решения, `> 0`). Неверное значение останавливает процесс на старте.
- Get Order использует тот же порт и возвращает `cancellation_window { opens_at, closes_at }` для `Order`, Order State которого допускает Order Cancellation по FSM (`placed`, `confirmed`). Для `cancelled` и `completed` поле отсутствует. Чтение не зависит от текущего времени: открыто ли окно сейчас, решает потребитель по своему clock, а authoritative check остаётся в Request Order Cancellation.
- Ошибка политики не ломает Get Order: окно не возвращается, ошибка пишется в log и span.
- Доменная модель получает чистую проверку «Order State допускает Cancellation» поверх transition table; `CheckCancellation` переиспользует её.
- В `nomnom.orders.v1.Order` добавляется поле `cancellation_window` (message `CancellationWindow`); изменение обратно совместимо по `buf breaking`.
- Обновить README `orders`, `config.example.yaml`, Helm values и раздел customer contract в README BFF (как BFF выводит `availableUntil`).

Не входит: публикация Request Order Cancellation в gRPC и подключение use case в DI, resolvers BFF, продуктовое решение о длительности окна и о моменте его открытия (например, только до подтверждения рестораном), окно в List Customer Orders.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `order-use-cases`: Get Order возвращает окно отмены; окно задаётся конфигурируемой cancellation policy.

## Impact

- `boundaries/orders`: `domains/order` (проверка по FSM), `applications/getorder` (порт политики), новый адаптер политики в `slices/order/infrastructure`, `pkg/config`, `slices/order/di`, `infrastructure/grpc` (proto, mapping), Helm values, README.
- Потребители `OrdersService`: новое необязательное поле, существующие клиенты не ломаются.
- Хранилище, миграции, Outbox, Temporal workflow и другие bounded contexts не меняются.
