# Spec Delta

## ADDED Requirements

### Requirement: Place Order принимает окно доставки
**ID:** FR-681

Place Order SHALL принимать необязательный `delivery_window_start`. Заданное окно вместе с `priority = PREMIUM` MUST давать `INVALID_ARGUMENT`. После записи relationship и до локальной транзакции Place Order SHALL резервировать окно в `restaurants` с заранее сгенерированным `order_id`, сохранять в `Order` окно из ответа резервации и передавать его в `OrderPlaced`.

#### Scenario: Заказ ко времени
**ID:** AC-912

- **WHEN** Customer оформляет заказ с `delivery_window_start` 19:00 в ресторане со свободным окном
- **THEN** `Order` сохранён с окном 19:00–19:15, а `OrderPlaced` несёт окно

#### Scenario: Окно и приоритет
**ID:** AC-914

- **WHEN** Customer оформляет заказ с окном и `priority = PREMIUM`
- **THEN** оформление отклоняется `INVALID_ARGUMENT` до резервации

### Requirement: Отказ резервации и повтор Place Order
**ID:** FR-692

Отказ резервации SHALL возвращаться из Place Order с её reason и альтернативными окнами; `Order` MUST NOT сохраняться, Outbox MUST NOT пополняться. Повтор с тем же idempotency key MUST возвращать сохранённый `Order` без новой резервации.

#### Scenario: Окно занято
**ID:** AC-913

- **WHEN** Customer оформляет заказ в заполненное окно
- **THEN** оформление отклоняется `SLOT_UNAVAILABLE` с альтернативными окнами, `Order` не сохранён, Outbox не пополнен

#### Scenario: Повтор оформления
**ID:** AC-915

- **WHEN** Place Order запланированного заказа повторяется с тем же idempotency key
- **THEN** возвращается тот же `order_id` с `replayed=true`, новая резервация не создаётся

## MODIFIED Requirements

### Requirement: Cancellation policy задаётся конфигурацией
**ID:** FR-462

Production cancellation policy SHALL открывать окно в момент Place Order и закрывать его через `cancellation.window`. Для запланированного заказа окно SHALL закрываться в `window_start − CANCELLATION__SCHEDULED_CUTOFF`. Неположительная или неразбираемая длительность любого из параметров MUST останавливать процесс на старте, а тестовые адаптеры `AlwaysOpen` и `FixedWindow` MUST NOT использоваться в production composition root.

#### Scenario: Неверная длительность окна
**ID:** AC-506

- **WHEN** процесс `orders` стартует с `CANCELLATION__WINDOW=0s`
- **THEN** загрузка конфигурации завершается ошибкой, и процесс не начинает обслуживать запросы

#### Scenario: Отмена запланированного заказа
**ID:** AC-916

- **GIVEN** окно заказа 19:00, `CANCELLATION__SCHEDULED_CUTOFF` 60 минут
- **WHEN** Customer отменяет заказ в 17:30, а затем другой такой же заказ в 18:10
- **THEN** первая отмена принимается, вторая отклоняется как закрытое окно отмены
