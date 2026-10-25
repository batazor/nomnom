# Tasks

## 1. restaurants

- [ ] 1.1 `ListRestaurants` в proto, генерация, обработчик поверх use case, тесты (`FR-496`, `AC-565`); README.

## 2. orders

- [ ] 2.1 Адаптер каталога на `GetRestaurant`/`GetMenu` через internal gateway, `ErrRestaurantNotAcceptingOrders`, конфигурация `catalog.*`, DI вместо `UnavailableCatalog`; тесты на bufconn (`FR-497`, `AC-566`); перегенерировать клиент restaurants.
- [ ] 2.2 `PlaceOrder`: `ORDER_NOT_ORDERABLE` с metadata `codes`/`menu_item_ids`, `RESTAURANT_NOT_ACCEPTING_ORDERS`; тесты сервиса (`FR-498`, `AC-567`); README, Helm values.

## 3. BFF

- [ ] 3.1 Перегенерировать клиенты; слайс `catalog` и resolvers `restaurants`, `restaurant` (`FR-499`, `AC-568`).
- [ ] 3.2 Слайс `checkout` и resolver `placeOrder` с переводом отказов (`FR-500`, `AC-569`, `AC-570`); README.

## 4. UI

- [ ] 4.1 `browse-restaurants` на `/`, `view-restaurant` с корзиной, `place-order` с idempotency key и problems, переход на заказ; удалить preview; i18n, доступность, тесты на MSW (`FR-501`, `AC-571`–`AC-573`); README и `docs/ui.md`.

## 5. Проверка

- [ ] 5.1 Тесты и линтеры всех затронутых приложений; `openspec validate add-customer-checkout-flow --strict`.
