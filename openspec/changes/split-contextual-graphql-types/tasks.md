# Tasks

## 1. BFF

- [x] 1.1 Схема: `RestaurantSummary` с connection, `OrderSummary` с connection, `Restaurant.waitTime: RestaurantWaitTime!`, описания полей без оговорок о месте чтения; `npm run generate` (`FR-450`, `FR-670`, `FR-671`).
- [x] 1.2 Presenters: `Order.restaurant`, `Kitchen.restaurant`, `myRestaurants`, `RestaurantOpeningPayload` — `RestaurantSummary`; `customerOrders` — `OrderSummary`; тесты (`AC-891`–`AC-893`).
- [x] 1.3 `npm run compose`: `config.json`, `client-schema.graphql`; persisted operations проходят `--check`.

## 2. UI

- [x] 2.1 `npm run codegen`; `MockRestaurantSummary`, `restaurantSummary`, `MockOrder.restaurant`, `waitTime` фикстур каталога не nullable; store, fulfillment, тест открытия ресторана.

## 3. Документация

- [x] 3.1 README `bff` (слайсы `catalog`, `ordertracking`, `restaurantmanagement`, `kitchen`), delta FR-647 change `add-kitchen-wait-time`.

## 4. Проверка

- [x] 4.1 `make check` в `boundaries/bff`, `go test ./...` plugin, `npm run check` UI, `openspec validate split-contextual-graphql-types --strict`.
