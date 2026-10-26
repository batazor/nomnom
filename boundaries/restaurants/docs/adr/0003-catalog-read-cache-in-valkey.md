# 3. Кеш чтения каталога restaurants в Valkey

Date: 2026-10-26

## Status

Accepted

Вводит кеш по [общесистемному ADR 40](../../../../docs/adr/0040-application-caching-policy.md).

## Context

`GetRestaurant`, `GetMenu` и `ListRestaurants` — публичные чтения без authorization, и их вызывают
чаще всего: UI через BFF при открытии списка и ресторана, BFF при разрешении сущностей, Place
Order контекста `orders` — `GetRestaurant` и `GetMenu` на каждый заказ. Каждый вызов — запрос в
MongoDB. Каталог меняется редко: владелец открывает и закрывает ресторан, правит меню, новый
ресторан появляется ещё реже. Короткое устаревание публичного представления допустимо: Place
Order и так читает каталог до своей транзакции, не атомарно с `restaurants`.

## Decision

### Что кешируется

| Чтение | Ключ Valkey |
| --- | --- |
| `GetRestaurant` (`RestaurantView`) | `restaurants:restaurant:<restaurantId>` |
| `GetMenu` (`MenuView`) | `restaurants:menu:<restaurantId>` |
| `ListRestaurants` (страница) | `restaurants:list:<open\|all>:<limit>:<after>` |

`ListOwnedRestaurants`, кухонные чтения и команды кеш не используют. Отсутствующий ресторан или
меню (`NOT_FOUND`) не кешируется.

### Только TTL, без инвалидации

`pkg/infrastructure/valkey/read-cache.ts`: промах читает MongoDB и кладёт JSON результата с
`CACHE__TTL` (1m), не задерживая ответ (запись не ожидается). Записи каталога кеш не трогают:
изменение — открытие, закрытие, цена, доступность блюда, новый ресторан — видно не позже чем
через TTL. Этого достаточно для данных, которые меняются раз в часы, и не требует ни записей
Outbox, ни consumer'а инвалидации.

Чем это оплачено:

- закрытый ресторан до TTL виден открытым: Place Order может принять заказ, ресторан его
  отклонит штатным Reject;
- новая цена попадает в заказ до TTL позже;
- после открытия или закрытия kitchen board (subscription `kitchenChanged`) и витрина до TTL
  показывают прежнее `isOpen`; результат самой команды Open/Close Restaurant приходит из команды,
  а не из кеша.

Если окно станет неприемлемым, сначала уменьшается `CACHE__TTL`; инвалидация через CDC (ADR 40)
вводится отдельным решением.

### Valkey и отказ

- Сервер — Valkey 9.1, ACL-пользователь `restaurants` с ключами `restaurants:*`
  (`+@all -@admin -@dangerous +info +client|setname`), без persistence, `allkeys-lru`.
- Клиент — [iovalkey](https://github.com/valkey-io/iovalkey) (форк ioredis организации Valkey):
  чистый JS, `commandTimeout`, без offline queue и без повторов команды.
- `VALKEY__URL` обязателен: пустой — `restaurants` не стартует. `restaurants-consumer` кеш не
  использует. Недоступный Valkey процесс не останавливает: пока соединение не `ready`, чтения
  сразу идут в MongoDB (`bypass`), команда ограничена `VALKEY__COMMAND_TIMEOUT` (50ms), клиент
  переподключается сам. Readiness от Valkey не зависит.
- Метрики `nomnom.cache.lookups` (`hit`/`miss`/`bypass`) и `nomnom.cache.errors` (`get`/`set`)
  по `nomnom.cache.name` (`restaurant`, `menu`, `restaurant_list`), без ключей и id.

## Consequences

- Повторные чтения каталога в пределах TTL не обращаются к MongoDB.
- Каталог устаревает до `CACHE__TTL` после изменения; это видно пользователю, как описано выше.
- Отказ Valkey — рост латентности и нагрузки на MongoDB, не ошибки RPC; доля `bypass` видна в
  метриках.
- В kind Valkey ставит chart `ops/kind/bootstrap` (`emptyDir`), URL — Secret `restaurants-valkey`.
