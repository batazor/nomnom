# Register Courier

## Сценарий

- **Тип:** Command.
- **Actor:** Оператор или диспетчер Courier Pool (`platform#manage_couriers`, FR-620).
- **Намерение:** Создать курьера в системе доставки.
- **Результат:** Создан `Courier` с именем (`CourierName`: 1–64 символа после trim, FR-301) и исходным состоянием доступности.

## Границы

- **Владелец:** `delivery/slices/courier`.
- **Домен:** `domains/courier`.
- **Взаимодействие:** Authoritative authorization выполняется в `delivery`.

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-301.
- **Порты:** `CourierAuthorization` (`ManageCouriers` над `Platform`), `CourierRepository`.
- **Транзакция:** Без UoW: одиночная вставка; повтор идентификатора — `CourierAlreadyRegistered`.
- **Допущение:** Новый `Courier` создаётся `available` (существующее поведение `Courier::new`).

## Acceptance

BDD-сценарии размещаются в `acceptance/register_courier.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).

