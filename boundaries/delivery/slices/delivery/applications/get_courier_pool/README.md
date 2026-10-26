# Get Courier Pool

## Сценарий

- **Тип:** Query.
- **Actor:** Оператор или диспетчер Courier Pool (`platform#manage_couriers`, FR-620).
- **Намерение:** Увидеть линию и загрузку курьеров платформы.
- **Результат:** Pool Load — `size` (невыбывшие курьеры), `on_line` (available и busy), `busy`, `waiting` (Delivery в `Waiting`) — и невыбывшие курьеры по имени с pending offer и активной доставкой; состояние не меняется.

## Границы

- **Владелец:** `delivery/slices/delivery`: query читает offers и доставки, а `courier` не зависит от `delivery` (FR-486).
- **Домен:** `domains/delivery`, `courier::domains::courier`.
- **Взаимодействие:** Обновления пула BFF получает подпиской `courierPoolChanged` на Change Notification `CourierPool` (ADR 36).

## Реализация

- **Handler:** `mod.rs` — `Output`, `Handler::execute(subject)`; требования FR-621.
- **Порты:** `CourierAuthorization` (`ManageCouriers` над `Platform`), `CourierPoolReadModel`, `Clock` (offer pending, пока не истёк).

## Acceptance

FR-621, AC-792 change [`add-cook-and-courier-pools`](../../../../../../openspec/changes/add-cook-and-courier-pools/).

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).
