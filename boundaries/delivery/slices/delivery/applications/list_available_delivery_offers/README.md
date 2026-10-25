# List Available Delivery Offers

## Сценарий

- **Тип:** Query.
- **Actor:** Курьер.
- **Намерение:** Получить актуальные предложения, адресованные курьеру.
- **Результат:** Возвращён список непротухших `Delivery Offer` без изменения состояния.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** Читает query model и проверяет адресата offer.

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-313.
- **Порты:** `DeliveryAuthorization` (`ListAvailableDeliveryOffers` над `Courier(id)`), `PendingDeliveryOffersReadModel`, `Clock`.
- **Транзакция:** Query без UoW; просроченные offers отфильтровываются по `expiresAt` включительно.

## Acceptance

BDD-сценарии размещаются в `acceptance/list_available_delivery_offers.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).

