# Design

## Решения

1. **Один `RestaurantSummary` на все места вне каталога.** Заказ, кухня, список владельца и payload открытия читают одни и те же поля: `id name acceptingOrders`. Отдельные типы для владельца и для заказа пока ничего не дают. В заказе `acceptingOrders` — текущая открытость ресторана, а не состояние на момент оформления; её читает тот же `GetRestaurant`, что и раньше.
2. **`Restaurant` — только каталог.** `restaurants` и `restaurant(id)` всегда читают ожидание (`wait_time`, FR-647), поэтому `waitTime` — `RestaurantWaitTime!`. `menu` (ресторан без меню) и `premiumFee` (ресторан без премиума) остаются nullable: `null` у них значит «нет», а не «не читалось». Решение 3 change `add-restaurant-opening-management` («payload переиспользует `Restaurant`, `menu = null`») заменяется: payload несёт `RestaurantSummary`.
3. **`OrderSummary` для истории.** Поля — карточка истории: `id status endReason placedAt total priority restaurant: RestaurantSummary!`. `endReason` и `status` строятся теми же таблицами, что у `Order`. Слайс `ordertracking` не меняется: `ListCustomerOrders` и дальше строит `OrderView` из проекции `OrderDetails` `orders` без дополнительных вызовов, а presenter отдаёт из него только поля сводки. `Order` в истории больше не встречается, поэтому `courier` и `tracking` у `Order` — `null` только по состоянию доставки.
4. **Совместимость.** Клиенты схемы — только наш UI. Persisted operations выбирают те же поля, их тексты и хеши не меняются; `node scripts/persisted-operations.mjs --check` проверяет их против новой клиентской схемы. Меняются только имена типов в codegen UI.

## Моки UI

`MockRestaurantSummary` — `Pick` полей сводки из `MockRestaurant`; функция `restaurantSummary` в `restaurant-fixtures.ts` строит его из ресторана. Ею пользуются заказы фикстур и store (`MockOrder.restaurant`), кухня, `myRestaurants` и payload открытия. Контрактный тест фикстур проверяет заказы против `Order`, поэтому ресторан с меню внутри заказа даёт расхождение со схемой. Fixture каталога `waitTime` всегда заполнен.
