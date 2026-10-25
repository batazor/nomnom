# Proposal

## Why

`restaurants` публикует команды `OpenRestaurant` и `CloseRestaurant` (permission `restaurant#manage`, повтор — `changed = false`), а GRPCRoute `restaurants-v1-management` internal gateway уже маршрутизирует их. Но клиентского пути к ним нет: в публичной схеме BFF нет mutations открытия, а в UI нет экрана, где владелец видит, принимает ли ресторан заказы, и меняет это.

## What Changes

- **Схема BFF**: mutations `openRestaurant(restaurantId)` и `closeRestaurant(restaurantId)` с payload `RestaurantOpeningPayload { restaurant, changed }`; `restaurant.acceptingOrders` — состояние открытия, `menu = null`.
- **BFF**, новый слайс `restaurantmanagement`: resolvers требуют access token и вызывают `restaurants` от имени пользователя (service token BFF и user assertion, ADR 32); authorization остаётся у `restaurants`. Отказы — GraphQL errors: `NOT_FOUND`, `PERMISSION_DENIED`, `INVALID_ARGUMENT`, остальное — `UNAVAILABLE` без внутренних деталей.
- **UI**, feature `manage-restaurant` на `/manage/restaurants/:restaurantId`: состояние «Открыт/Закрыт», одно действие «Открыть/Закрыть ресторан» с pending-состоянием, сообщения об отказах, ручной повтор; MSW-моки и сгенерированные типы.

Не входит: список ресторанов владельца и навигация к экрану (нужен query, которого в `restaurants` нет), управление меню и персоналом, вход владельца (identity provider в браузере не выбран, как и для `placeOrder`), отдельное restaurant-приложение.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `customer-graphql-contract`: mutations открытия ресторана в публичной схеме.
- `customer-order-resolvers`: resolvers открытия ресторана от имени пользователя.
- `customer-web-application`: экран открытия ресторана владельцем; исключение из границы FR-001 до появления отдельного restaurant UI.

## Impact

`boundaries/bff/plugins/bff` (схема, generated-контракт, клиент `restaurants`, слайс, wiring, тесты), `boundaries/ui` (feature, router, i18n, моки, generated types), README BFF и UI, `docs/ui.md`. Схема расширяется обратно совместимо; gateway, Helm и kind не меняются: route `restaurants-v1-management` и `userDelegates: bff` у `restaurants` уже есть.
