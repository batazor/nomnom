# Design

## Доплата ресторана

- `Restaurant.premiumFee?: Money`. Update Restaurant принимает название и `premium_fee` с явной семантикой поля: задать сумму или убрать опцию (`clear_premium_fee`). Сумма MUST быть больше 0, валюта — ISO 4217.
- Новое gRPC `UpdateRestaurant` с той же authorization, что у Update Restaurant (`restaurant#manage`). Integration Event не публикуется, кеш каталога обновляется по TTL, как при переименовании (ADR 0003 restaurants).
- `Restaurant` в `restaurants.proto` получает `premium_fee` (не задано — опции нет).

Отсутствие доплаты — пустое состояние, а не подмена: опции просто нет.

## Place Order

- `PlaceOrderRequest.priority`: `ORDER_PRIORITY_STANDARD` / `ORDER_PRIORITY_PREMIUM`; не задан (`UNSPECIFIED`) — `STANDARD` по решению пользователя, неизвестное значение — `INVALID_ARGUMENT`.
- Catalog snapshot уже читает `GetRestaurant`; для премиума он берёт `premium_fee`:
  - доплаты нет — `FAILED_PRECONDITION`, reason `PREMIUM_UNAVAILABLE`;
  - валюта доплаты не совпадает с валютой позиций — та же ошибка, что и при смешении валют в `total` (арифметика Money только в одной валюте).
- Order хранит `priority` и `premium_fee` (снимок на момент Place Order) и включает доплату в `total`. Если ресторан потом поменяет доплату, оформленные заказы не изменятся.
- Workflow не меняет маршрут: Activity `RequestRestaurantOrder` передаёт `priority` из Order в `ReceiveOrderRequest`.

## Приоритет Restaurant Order

- Receive Order Request требует `priority`, без него — `INVALID_ARGUMENT`. Restaurant Order хранит его как данные, а не как State FSM.
- Migration ставит `STANDARD` существующим Restaurant Order: до change премиума не было, это факт, а не значение по умолчанию.

## Строгая очередь

`KitchenQueueOrdering` перестаёт быть рекомендацией: это правило, по которому `StartNextPreparation` выбирает заказ.

Класс заказа в очереди на момент `now`:

1. **первый** — `PREMIUM` или `STANDARD`, у которого `now − queued_at > B × KITCHEN_QUEUE__AGING_FACTOR`;
2. **второй** — остальные `STANDARD`.

Внутри класса — FIFO по `queued_at`, затем по id. `B` — базовое время ресторана из `add-kitchen-wait-time`, слайс `restaurantorder` читает его через порт. `KITCHEN_QUEUE__AGING_FACTOR` — обязательный конфиг (значение задаётся явно в values, например 2); пустой или невалидный — падение на старте.

## Start Next Preparation

`StartNextPreparation(restaurant_id, request_id)` заменяет `StartPreparation`. В одной UoW команда (`NoCookAvailable` — `RESOURCE_EXHAUSTED`, как и раньше):

1. Ищет Restaurant Order ресторана с этим `start_request_id`. Нашёлся — это повтор: срабатывает политика повтора composition root (успешный no-op с текущим заказом или already in state), как в FR-225.
2. Читает очередь и выбирает голову по правилу выше. Очередь пуста — пустой результат: ничего не начато, ответ без `restaurant_order`, Outbox не меняется. Это нормальное состояние кухни, а не отказ.
3. Назначает свободного Cook (`add-cook-and-courier-pools`). Нет Cook — `NoCookAvailable`, заказ остаётся в `QUEUED`.
4. Переводит заказ в `PREPARING` с `start_request_id` и записывает `PreparationStarted`.

`request_id` обязателен: без него повтор после сетевой ошибки взял бы следующий заказ. Две одновременные команды могут выбрать одну голову — вторая получает write conflict и повторяется клиентом с тем же `request_id`, после чего берёт следующий заказ.

Уникальный индекс `(restaurantId, startRequestId)` с partial filter.

## UI

- Оформление заказа: чекбокс «Приоритет — приготовим первым, +N» виден, только если у `Restaurant.premiumFee` есть значение. Сумма заказа показывается с доплатой.
- Экран заказа: метка «Приоритет».
- Флоу-борд, колонка кухни: заказы в `QUEUED` идут в порядке Get Kitchen Queue, у карточек нет действия, над колонкой одна кнопка «Взять следующий» (`START_NEXT_PREPARATION`). На премиум-карточках — метка «Приоритет».
- Симулятор кухни вызывает `StartNextPreparation`.

## Риски

- BREAKING `StartPreparation`: потребители — BFF, симулятор, kind e2e. Все меняются в этом change. `buf breaking` ожидаемо падает один раз.
- Activity `RequestRestaurantOrder` получает новое поле. Маршрут workflow не меняется, но в kind перед выкаткой worker'а нужно прервать незавершённые workflow, как в прошлых change.
- Если почти все заказы премиальные, приоритет ничего не ускоряет. Обещание — только «первым в очереди», без срока.
