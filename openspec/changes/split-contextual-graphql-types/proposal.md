# Proposal

## Why

В customer-схеме BFF `Restaurant` и `Order` заполнены по-разному в зависимости от того, где их прочитали:

- у `Restaurant` поля `menu`, `waitTime` и `premiumFee` заполняют только запросы каталога `restaurants` и `restaurant`. В `Order.restaurant`, `Kitchen.restaurant`, `myRestaurants` и `RestaurantOpeningPayload` они всегда `null`;
- у `Order` поля `courier` и `tracking` всегда `null` в `customerOrders`, а в `order(id)` и `orderChanged` — по состоянию доставки.

По схеме клиент не может отличить «данных нет» от «здесь это поле не читается». Описания полей вынуждены перечислять запросы («set by the catalog queries, null elsewhere»), а `waitTime` в каталоге объявлен nullable, хотя каталог отдаёт его всегда.

Заполнять поля везде одинаково нельзя:

- **Лишние вызовы.** `order(id)`, `kitchen` и каждое событие `orderChanged`/`kitchenChanged` вызывали бы `GetMenu` и читали бы ожидание. Этих полей не читает ни одна операция UI. `customerOrders` вызывал бы `GetOrderDelivery` на каждый заказ страницы: пакетного чтения доставки нет, получился бы N+1 (ADR 2).
- **Путаница смысла.** `Order.restaurant.premiumFee` (текущая цена) стоял бы рядом с `Order.premiumFee`, зафиксированным при оформлении.

## What Changes

- **`RestaurantSummary { id name acceptingOrders }`** — ресторан вне каталога: `Order.restaurant`, `Kitchen.restaurant`, `myRestaurants`, `RestaurantOpeningPayload.restaurant`. `myRestaurants` возвращает `RestaurantSummaryConnection`.
- **`Restaurant`** остаётся типом каталога. `waitTime` становится `RestaurantWaitTime!`. `menu` и `premiumFee` остаются nullable по своим причинам: меню не опубликовано, премиума нет.
- **`OrderSummary { id status endReason placedAt total priority restaurant }`** — узел `customerOrders`, который возвращает `OrderSummaryConnection`. Подробности заказа клиент читает через `order(id)`.
- У `Order` поля `courier` и `tracking` становятся `null` только по состоянию заказа. Оговорка про `customerOrders` из описаний уходит.
- **BREAKING** для клиентов схемы: меняются имена типов. Тексты persisted operations не меняются — фрагментов `on Restaurant` и `on Order` с этими полями в них нет, выбранные поля сохраняются.

Не входит:

- новые поля в summary-типах: их добавят, когда они понадобятся экрану;
- изменения контрактов `orders`, `restaurants` и `delivery`.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `customer-graphql-contract`: типы ресторана и заказа по контексту, `customerOrders` возвращает `OrderSummary`.

## Impact

- **`boundaries/bff`:** `schema.graphql`, generated plugin, presenters `order_presenter.go`, `kitchen_presenter.go`, `restaurantmanagement_presenter.go`, `catalog_presenter.go`, тесты, `config.json`, `client-schema.graphql`, README.
- **`boundaries/ui`:** generated types, mock fixtures и store (`MockRestaurantSummary`, `MockOrder.restaurant`), тест открытия ресторана.
- **Документация:** delta FR-647 change `add-kitchen-wait-time` (ожидание больше не «`null` в остальных местах»).
