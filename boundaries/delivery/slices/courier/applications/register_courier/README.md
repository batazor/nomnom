# Register Courier

## Сценарий

- **Тип:** Command.
- **Actor:** Оператор платформы.
- **Намерение:** Создать курьера в системе доставки.
- **Результат:** Создан `Courier` с исходным состоянием доступности.

## Границы

- **Владелец:** `delivery/slices/courier`.
- **Домен:** `domains/courier`.
- **Взаимодействие:** Authoritative authorization выполняется в `delivery`.

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-301.
- **Порты:** `CourierAuthorization` (`RegisterCourier` над `Platform`), `CourierRepository`.
- **Транзакция:** Без UoW: одиночная вставка; повтор идентификатора — `CourierAlreadyRegistered`.
- **Допущение:** Новый `Courier` создаётся `available` (существующее поведение `Courier::new`).

## Acceptance

BDD-сценарии размещаются в `acceptance/register_courier.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).

