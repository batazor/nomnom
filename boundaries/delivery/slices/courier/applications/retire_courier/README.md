# Retire Courier

## Сценарий

- **Тип:** Command.
- **Actor:** Оператор или диспетчер Courier Pool (`platform#manage_couriers`, FR-620).
- **Намерение:** Убрать курьера из Courier Pool.
- **Результат:** `Courier` выбыл (`retired_at`) и больше не получает Delivery Offer и Assignment; повтор — no-op с `changed: false`.

## Границы

- **Владелец:** `delivery/slices/courier`.
- **Домен:** `domains/courier`.
- **Взаимодействие:** Запись курьера пишет Change Notification курьера и Courier Pool (ADR 36).

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-619.
- **Порты:** `CourierAuthorization` (`ManageCouriers` над `Platform`), `CourierRepository`, `Clock`.
- **Транзакция:** Без UoW: одиночная запись.
- **Допущение:** Выбыть может только курьер вне линии (`unavailable`); available или busy — `OnLine`. Выбывший курьер остаётся в истории доставок и на линию не возвращается.

## Acceptance

FR-619, AC-788, AC-789 change [`add-cook-and-courier-pools`](../../../../../../openspec/changes/add-cook-and-courier-pools/).

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).
