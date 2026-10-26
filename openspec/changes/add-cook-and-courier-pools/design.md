# Design

## Термины

- **Cook** — повар ресторана с именем: единица мощности кухни. Не subject и не relation `staff`: `staff` решает, кто вправе выполнять команды кухни, Cook — кто готовит заказ. Команды по-прежнему выполняет subject с `restaurant#operate_kitchen`.
- **Cook Pool** — все невыбывшие Cook ресторана. **Courier Pool** — все невыбывшие Courier платформы.
- **On Line** — работник принимает работу. Cook на линии или вне её; Courier на линии в `available` и `busy`, вне линии — в `unavailable`.
- **Retire** — вывод работника из пула. Допустим только вне линии; запись остаётся (на неё ссылаются заказы и доставки), но работник больше не входит в пул и не может вернуться — нужен новый.
- **Pool Load** — `busy / on_line` плюс backlog — работа, которая ждёт свободного работника.

## Cook Pool (`restaurants`)

Слайс `cook`, aggregate `Cook`: `id`, `restaurantId`, `name`, `onLine`, `lastAssignedAt`, `retiredAt`, `version`. Новый Cook вне линии.

| Use case | Предусловие | Результат |
| --- | --- | --- |
| Hire Cook | Непустое имя после trim, не длиннее 64 символов | Cook вне линии |
| Put Cook On Line | Cook не выбыл | `onLine = true`; повтор — no-op |
| Take Cook Off Line | Cook не держит заказ | `onLine = false`; повтор — no-op; держит заказ — `CookBusy` |
| Retire Cook | Cook вне линии | `retiredAt`; на линии — `CookOnLine`; повтор — no-op |
| Get Cook Pool | — | Pool Load и Cook с текущим заказом |

Права — `restaurant#manage_cooks = owner + staff`; Get Cook Pool — `restaurant#view_orders`. Изменения Cook публикуют Change Notification ресторана, как изменения его заказов, поэтому `kitchenChanged(restaurantId)` приходит и после них.

### Занятость выводится из заказов

Cook занят, если есть Restaurant Order в `PREPARING` или `ASSEMBLED` с его `cookId`. Флага `busy` в Cook нет: иначе каждый переход в `READY`, `FAILED` и `CANCELLED` менял бы два aggregate, и состояние повара могло бы разойтись с заказами. `cookId` остаётся в заказе и после завершения — это история «кто готовил».

### Назначение при Start Preparation

Start Preparation в одной UoW (ADR 20):

1. выбирает свободного Cook на линии с наименьшим `lastAssignedAt` (никогда не назначенные первыми, затем по `id`);
2. вызывает `cook.assign(now)` — `lastAssignedAt = now`, version растёт;
3. переводит заказ в `PREPARING` с `cookId` и пишет `PreparationStarted`.

Нет свободного Cook — бизнес-отказ `NoCookAvailable`, заказ остаётся в `QUEUED`, Outbox не меняется. gRPC: `RESOURCE_EXHAUSTED` — `FAILED_PRECONDITION` у `StartPreparation` уже означает недопустимый переход, а BFF различает исходы по коду.

Гонки:

- два Start Preparation выбирают одного Cook — оба пишут его документ, snapshot-транзакция MongoDB даёт write conflict, проигравший повторяется и выбирает другого или получает `NoCookAvailable`. Unique partial index `restaurant_orders { cookId: 1 }` с `partialFilterExpression: { state: { $in: ["PREPARING", "ASSEMBLED"] } }` — инвариант «один Cook — один активный заказ» на уровне БД;
- Take Cook Off Line одновременно с назначением — Take Cook Off Line в той же UoW проверяет отсутствие активного заказа и сохраняет Cook по version, поэтому одна из транзакций получает конфликт.

### Пустая линия

Acceptance от линии не зависит: заказ принимается и ждёт в `QUEUED`. Если никто не встанет на линию, срабатывает таймер ожидания Preparation workflow `orders` ([ADR 5 orders](../../../boundaries/orders/docs/adr/0005-order-fulfillment-orchestration.md)) и ведёт к бизнес-ветке отмены. UI показывает пустую линию как предупреждение.

### Хранение

Коллекция `cooks` (набор миграций `cooks`, слайс `cook`): `$jsonSchema` с обязательными `restaurantId`, `name`, `onLine`; индекс `{ restaurantId: 1, retiredAt: 1, onLine: 1, lastAssignedAt: 1 }` для выбора свободного и чтения пула. `restaurant_orders` получает `cookId`, обязательный в `PREPARING` и `ASSEMBLED` (validator по `state`), новый `schemaVersion` и unique partial index. Активные заказы без `cookId` новая схема не принимает: хранилища kind — emptyDir и пересоздаются вместе с seed; других окружений с данными нет.

## Courier Pool (`delivery`)

- `Courier.name` — обязательный, с теми же правилами, что у Cook; задаётся в Register Courier. Пустое имя — `InvalidArgument`.
- Retire Courier: Courier `unavailable` → `retired_at`; `available` или `busy` — отказ `CourierOnLine`. Выбывший Courier не получает офферов (locator берёт только невыбывших) и не меняет доступность.
- Set Courier Availability — как прежде; кроме `courier#act` его разрешает `platform#manage_couriers = operator + dispatcher`: use case сначала проверяет право курьера, при отказе — право диспетчера, поэтому связей курьера с платформой в SpiceDB не нужно. Register Courier и Retire Courier тоже требуют `platform#manage_couriers`; `register_courier` удалён.
- Get Courier Pool — `platform#manage_couriers`: Pool Load (backlog — Delivery в `Waiting`) и Courier с именем, доступностью, pending offer и активной Delivery. Query читает offers и доставки, поэтому живёт в слайсе `delivery` (зависимость только `delivery → courier`, FR-486), как Get Courier Current Delivery.
- Каждая запись курьера, оффера и Delivery пишет в Outbox Change Notification `{"__typename":"CourierPool","id":"nomnom"}` в топик `delivery.v1.courier-pool-changes`, на которой строится `courierPoolChanged`; у курьера — в том же SQL-операторе, что и `CourierChanged`.
- Миграция `couriers/0002`: `name TEXT NOT NULL` без default (trim, 1..64), `retired_at BIGINT` — epoch seconds, как остальные времена `delivery`, — и проверка «выбывший — `unavailable`»; хранилище kind пересоздаётся.

Курьеры, добавленные из UI, не связаны с пользователем: BFF выдаёт им UUIDv7 как `courier_id`. Действует за них только Simulator; при выключенном Simulator их офферы истекают и Delivery переходит к следующему кандидату.

## Simulator

- Кухня выполняет запланированный `start_preparation`, только если в пуле есть свободный Cook; на `NoCookAvailable` шаг остаётся due и повторяется на следующем тике без лога ошибки. После старта оставшиеся шаги планируются заново от фактического старта; более поздний READY, чем `expectedReadyAt`, становится Report Preparation Delay, как сейчас.
- Simulator не меняет состав пула и линию — это действие пользователя.
- Simulator `delivery` по-прежнему действует за любого курьера с оффером или активной Delivery.

## BFF и UI

- `Kitchen.cookPool: CookPool!` (`size`, `onLine`, `busy`, `queued`, `cooks[]` с `id`, `name`, `onLine`, `currentOrder`), mutations `hireCook(restaurantId, name)`, `setCookOnLine(cookId, onLine)`, `retireCook(cookId)`; `KitchenOrder.cook: CookRef` в активных и готовых заказах.
- `courierPool: CourierPool!` (`size`, `onLine`, `busy`, `waiting`, `couriers[]`), `courierPoolChanged: CourierPool!`, mutations `addCourier(name)`, `setCourierOnLine(courierId, onLine)`, `retireCourier(courierId)`; имя курьера в карточке доставки и в `Order` покупателя.
- Имена разрешает BFF по id; в Integration Events имена не передаются. Имя курьера покупателю `delivery` пока отдаёт только диспетчеру (`GetCourierPool`): для экрана заказа в разделе 4 нужен read, доступный покупателю заказа.
- Шапка колонки: «Линия N из M · занято B/N» с полосой загрузки, backlog отдельной строкой; при `onLine = 0` и непустой очереди — предупреждение «на линии нет поваров». Список работников: точка статуса, имя, текущая работа, действие. Колонка курьера показывает пул, только если `courierPool` не отвечает `PermissionDenied`; собственная работа курьера-пользователя остаётся как прежде.
- Покупатель видит имя курьера, имя повара — нет.

## Ограничения

- Назначение только автоматическое; ручной выбор исполнителя — отдельный change, для курьеров он меняет модель офферов.
- `expected_ready_at` по-прежнему задаёт Acceptance (в demo — план Simulator); учёт очереди и линии — второй этап.
- Курьер держит не больше одного Assignment, как сейчас.
