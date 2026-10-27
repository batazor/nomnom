# 3. Кеш чтения каталога restaurants в Valkey

Date: 2026-10-26

## Status

Accepted

Вводит кеш по [общесистемному ADR 43](../../../../docs/adr/0043-application-caching-policy.md).

## Context

`GetRestaurant`, `GetMenu` и `ListRestaurants` — публичные чтения без authorization, и их вызывают
чаще всего: UI через BFF при открытии списка и ресторана, BFF при разрешении сущностей, Place
Order контекста `orders` — `GetRestaurant` и `GetMenu` на каждый заказ. Каждый вызов — запрос в
MongoDB. Каталог меняется редко: владелец открывает и закрывает ресторан, правит меню, новый
ресторан появляется ещё реже. Короткое устаревание публичного представления допустимо: Place
Order и так читает каталог до своей транзакции, не атомарно с `restaurants`.

## Decision

### Что кешируется

| Чтение | Ключ Valkey | Обновляется сразу по |
| --- | --- | --- |
| `GetRestaurant`, `BatchGetRestaurants` (`RestaurantView` с ожиданием) | `restaurants:restaurant:<restaurantId>` | `RestaurantOpened`, `RestaurantClosed`, `RestaurantWaitLevelChanged` |
| `GetMenu`, `BatchGetMenus` (`MenuView`) | `restaurants:menu:<restaurantId>` | `MenuItemAdded`, `MenuItemUpdated`, `MenuItemAvailabilityChanged` |
| `ListRestaurants` (страница) | `restaurants:list:<поколение Market>:<marketId>:<open\|all>:<limit>:<after>` | `RestaurantOpened`, `RestaurantClosed`, `RestaurantWaitLevelChanged` (новое поколение Market ресторана) |

`ListOwnedRestaurants`, кухонные чтения и команды кеш не используют. Отсутствующий ресторан или
меню (`NOT_FOUND`) не кешируется.

### Заполнение и обновление по событиям

`pkg/infrastructure/valkey/read-cache.ts`: промах читает MongoDB и кладёт JSON результата
`SET NX` с `CACHE__TTL` (1m), не задерживая ответ.
`BatchGetRestaurants` и `BatchGetMenus` (ADR 7) читают те же ключи одним `MGET` (`ReadCache.readMany`),
все промахи пачки — одним запросом MongoDB по `$in`, и кладут каждый найденный результат `SET NX`
с тем же TTL; пачка не читается ключ за ключом.

Открытие, закрытие, новое блюдо, цену, название и доступность блюда пользователь должен видеть
сразу, поэтому кеш обновляется по Integration Events, которые `restaurants` публикует через
Outbox и Debezium: `RestaurantOpened`/`RestaurantClosed` и смену уровня ожидания
`RestaurantWaitLevelChanged` (`restaurants.v1.restaurant-events`) и
`MenuItemAdded`, `MenuItemUpdated`, `MenuItemAvailabilityChanged` (`restaurants.v1.menu-events`;
`MenuItemAdded` и `MenuItemUpdated` добавлены в контракт ради этого, FR-613). `restaurants-consumer` читает их
своей consumer group `CACHE__REFRESH_GROUP` (`restaurants-cache`, новая группа начинает с конца
стрима; `pkg/infrastructure/valkey/refresh.ts`), перечитывает ресторан или меню из MongoDB уже
после commit и перезаписывает ключ обычным `SET` с TTL. Для ресторана ещё увеличивается
поколение страниц списка его Market (`INCR restaurants:list:generation:pages:<marketId>`, Market — из
сохранённого ресторана; [ADR 41](../../../../docs/adr/0041-cell-based-architecture-with-control-plane.md):
Market входит в ключ и в поколение, поэтому страницы разных Market не смешиваются, а изменение
ресторана сбрасывает только страницы его Market): страницу, где ресторан
лежит, по одному ключу не найти, а страницы старого поколения больше не читаются и истекают по
TTL.

Минуты ожидания в представлении ресторана считаются при чтении из Wait Time Statistics
(`docs/kitchen.md#wait-time-statistics`), поэтому в кеше они устаревают не больше чем на
`CACHE__TTL`; сразу обновляется только смена уровня. Запись без ожидания, закешированная до
него, — промах, как запись без Market.

Гонка «чтение началось до изменения» безопасна без версий:

- чтение успело записать старое значение раньше обновления — обновление его перезаписывает;
- обновление записало раньше — `SET NX` старого чтения не срабатывает;
- страницу старого чтения пишет старое поколение, которое уже никто не читает.

Обновление перечитывает текущий документ, поэтому повторы at-least-once и события подряд
(закрыли и сразу открыли) безопасны без Inbox. Пока Valkey недоступен, обработка повторяется с
backoff и offset не коммитится: после восстановления все обновления применяются.

Изменения без Integration Event — переименование ресторана, новый ресторан — видны не позже
TTL. Если это окно станет неприемлемым, нужен контракт события
для такого изменения, а не меньший TTL.

### Valkey и отказ

- Сервер — Valkey 9.1, ACL-пользователь `restaurants` с ключами `restaurants:*`
  (`+@all -@admin -@dangerous +info +client|setname`), без persistence, `allkeys-lru`.
- Клиент — [iovalkey](https://github.com/valkey-io/iovalkey) (форк ioredis организации Valkey):
  чистый JS, `commandTimeout`, без offline queue и без повторов команды.
- `VALKEY__URL` обязателен: пустой — `restaurants` и `restaurants-consumer` не стартуют.
  Недоступный Valkey процесс не останавливает: пока соединение не `ready`, чтения
  сразу идут в MongoDB (`bypass`), команда ограничена `VALKEY__COMMAND_TIMEOUT` (50ms), клиент
  переподключается сам. Readiness от Valkey не зависит.
- Метрики `nomnom.cache.lookups` (`hit`/`miss`/`bypass`), `nomnom.cache.errors`
  (`get`/`set`/`refresh`) и `nomnom.cache.refreshes` по `nomnom.cache.name` (`restaurant`, `menu`, `restaurant_list`), без ключей и id.

## Consequences

- Повторные чтения каталога не обращаются к MongoDB.
- Открытие, закрытие ресторана и изменения меню видны в кеше через отставание CDC (alert
  `cdc-lag`) плюс обработку consumer'а; остальные изменения — до `CACHE__TTL`.
- `restaurants-consumer` зависит от Valkey: без него обновления ждут, offsets стоят.
- Отказ Valkey — рост латентности и нагрузки на MongoDB, не ошибки RPC; доля `bypass` видна в
  метриках.
- В kind Valkey ставит chart `ops/kind/bootstrap` (`emptyDir`), URL — Secret `restaurants-valkey`.
