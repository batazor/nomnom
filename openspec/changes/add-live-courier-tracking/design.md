# Design

## Virtual City

Virtual City — value object домена `delivery` (слайс `delivery`): квадратная сетка `blocks × blocks` кварталов со стороной `block_meters`, юго-западный угол — `(origin_latitude, origin_longitude)`. Перекрёсток `(i, j)`, где `0 ≤ i, j ≤ blocks`, переводится в координаты так:

- `latitude = origin_latitude + j × block_meters / 111 320`;
- `longitude = origin_longitude + i × block_meters / (111 320 × cos(origin_latitude))`.

На масштабе города (несколько километров) этого приближения достаточно. Улицы — линии сетки. Обратное преобразование и «ближайший перекрёсток» — методы того же VO.

| Переменная | kind | Значение |
| --- | --- | --- |
| `TRACKING__CITY_ORIGIN_LATITUDE` | `52.5150` | широта юго-западного угла |
| `TRACKING__CITY_ORIGIN_LONGITUDE` | `13.3850` | долгота юго-западного угла |
| `TRACKING__CITY_BLOCKS` | `12` | число кварталов по стороне |
| `TRACKING__BLOCK_METERS` | `150` | сторона квартала |
| `TRACKING__PERSIST_INTERVAL` | `15s` | минимальный интервал записи позиции в PostgreSQL |

Все переменные обязательны, значений по умолчанию нет. Пустое значение, нечисло, `blocks < 2`, неположительный размер или рамка за пределами диапазонов широты и долготы завершают startup. В kind рамка накрывает позиции курьеров из `ops/kind/demo/files/seed.json`. Virtual City одна на ячейку: Market координат не имеет (`controlplane` отдаёт только `id`, `name`, `currency`), а заводить их ради демо этот change не будет.

## Виртуальные точки доставки

Create Delivery выбирает два разных случайных перекрёстка, `pickup_point` и `dropoff_point`, и сохраняет их в `Delivery` (миграция `slices/delivery/.../migrations/0008_delivery_tracking.sql`: `pickup_x`, `pickup_y`, `dropoff_x`, `dropoff_y` — перекрёстки сетки, `tracking_channel`). Хранятся перекрёстки, а не координаты: `Delivery` остаётся `Eq`, а координаты считает Virtual City при чтении. Повтор Create Delivery по тому же `order_id` возвращает уже сохранённые точки. Доставки, созданные до миграции, точек и канала не имеют: для них `tracking` равен `null`, и они живут как прежде.

Генератор случайных чисел — порт (`RandomSource`), в тестах он детерминированный.

## Tracking Channel

`tracking_channel` — 128 случайных бит в 32 hex-символах (генератор ОС, `getrandom`), создаётся вместе с `Delivery`. Канал Valkey — `delivery.tracks.<tracking_channel>`.

Почему канал-капабилити, а не notification + refetch из ADR 36:

- при 1 Hz запрос за данными с проверкой прав означал бы один gRPC-вызов `delivery` и один check SpiceDB на каждую точку и каждого подписчика;
- проверить права при старте подписки без собственной сборки Router нельзя (ADR 36, `SubscriptionOnStart`).

Поэтому права проверяются один раз, когда выдаётся канал: `GetOrderTracking` требует `order#view` так же, как `GetOrderCourier` (FR-626). Подписаться без канала нельзя, а угадать 128 бит нереально. После `Delivered`, `Failed` или `Cancelled` Update Courier Location в канал больше не публикует, поэтому утёкший токен ничего не даёт.

## Update Courier Location

Handler после проверки прав и координат:

1. сохраняет позицию через `CourierLocationStore::record_thinned` условным upsert: `latitude`, `longitude`, `recorded_at` меняются, только если прежняя запись старше `TRACKING__PERSIST_INTERVAL`;
2. читает канал активной доставки курьера (`assigned` или `picked_up`) через порт `ActiveTrackingChannel` слайса `courier`.

Слайс `courier` не знает `Delivery` (FR-486), поэтому порт реализует адаптер верхнего уровня `infrastructure/tracks.rs`, как `StoredCourierLocator`: он читает `deliveries` по индексу `deliveries_courier`.

Если у курьера есть активная доставка с каналом, handler публикует в канал JSON:

```json
{"__typename":"CourierPosition","channel":"<tracking_channel>","latitude":52.52,"longitude":13.40,"recordedAt":"2026-10-27T12:00:00Z"}
```

`recordedAt` берётся из clock `delivery`. Порядок такой: запись в PostgreSQL, затем публикация. Ошибка публикации возвращает `UNAVAILABLE` без отката записи: позиция в PostgreSQL — прореженная копия, а следующий тик отправит свежую точку. Без активной доставки ничего не публикуется.

Прореживание меняет FR-303: сохранённая позиция теперь может быть старше последнего вызова, но не больше чем на `TRACKING__PERSIST_INTERVAL`. `CourierLocator` и так считает курьеров с известной позицией равными по расстоянию, поэтому выбор кандидата не меняется.

Valkey — порт `CourierPositionPublisher` слайса `courier`, адаптер в `infrastructure/tracks.rs` (`PUBLISH`). `VALKEY__URL` обязателен: пусто — ошибка startup. Это pub/sub, а не кеш ADR 43: ничего не хранится, TTL нет.

## Get Order Tracking

Query слайса `delivery`: `GetOrderTracking(order_id)` → `{ channel, city, pickup, dropoff, courier_position }`, только для доставки в `assigned` или `picked_up` с каналом. Иначе ответ пустой. `courier_position` — сохранённая позиция курьера с `recorded_at` (может быть старше на `TRACKING__PERSIST_INTERVAL`). `city` — параметры Virtual City (origin, blocks, block_meters), чтобы UI рисовал ту же сетку, что видит Simulator, без общих констант.

## BFF

Plugin резолвит `Order.tracking: OrderTracking` вызовом `GetOrderTracking` от имени покупателя (ADR 32). Поле `null` без активной доставки.

Events subgraph:

```graphql
courierMoved(channel: ID!): CourierPosition!
  @edfs__redisSubscribe(channels: ["delivery.tracks.{{ args.channel }}"], providerId: "tracks")
```

Все поля `CourierPosition` приходят в событии, поэтому Router не вызывает plugin. Провайдер `redis` с `id: tracks` и URL `BFF__TRACKS_REDIS_URL` (обязателен) добавляется в `config.yaml` и `config.production.yaml`. ACL-пользователь `bff` в Valkey может только подписываться (см. ниже).

Проверено на Router 0.363.0 (задача 0.1):

- шаблон `{{ args.channel }}` в `channels` провайдера Redis работает, чужой канал подписчику не приходит;
- event-driven subgraph может объявлять только поля ключа, а тип события должен быть сущностью. Поэтому ключ `CourierPosition` — все её поля: `@key(fields: "channel latitude longitude recordedAt", resolvable: false)` в events subgraph (поля `@external`) и в plugin (обычные поля, тот же тип отдаёт `OrderTracking.courierPosition`). Lookup RPC plugin для него не генерируется, Router берёт поля из события;
- Router подписывается `PSUBSCRIBE`, а ACL Valkey сравнивает шаблон PSUBSCRIBE буквально, поэтому у ACL-пользователя `bff` `allchannels` и только команды подписки (`SUBSCRIBE`, `PSUBSCRIBE` и их отмена, handshake), публиковать он не может.

## Simulator

`di::SimulatorWorker` уже планирует моменты Pickup и вручения (FR-610). К ним добавляется план движения:

- **Accept.** Старт — сохранённая позиция курьера, привязанная к ближайшему перекрёстку внутри Virtual City (позиция вне рамки прижимается к краю). Маршрут — случайный кратчайший путь по сетке до `pickup_point`: шаги по горизонтали и вертикали в случайном порядке. Длительность — до `expected_pickup_at`.
- **Pickup.** Маршрут от `pickup_point` до `dropoff_point` до `expected_delivered_at`.
- **Тик.** Каждые `SIMULATOR__TRACK_INTERVAL` для каждой движущейся доставки Simulator вычисляет точку на маршруте по доле прошедшего времени и вызывает Update Courier Location от имени курьера (`Subject::User(courier_id)`). Когда время вышло, курьер стоит в конечной точке до действия Pickup или вручения.

Тик позиций идёт отдельно от `SIMULATOR__INTERVAL` (5s в kind): движение должно быть плавнее, чем шаги жизненного цикла. Один цикл worker шагает каждые `SIMULATOR__TRACK_INTERVAL` и раз в `SIMULATOR__INTERVAL` сначала делает проход расписания; маршрут шага строится на проходе, который видит доставку в этом шаге, поэтому курьер трогается в пределах `SIMULATOR__INTERVAL` после Accept или Pickup. `SIMULATOR__TRACK_INTERVAL` обязателен, ноль или значение больше `SIMULATOR__INTERVAL` завершают startup (kind: 1s). Маршруты живут в памяти процесса, как и остальное расписание: после рестарта или выключения маршрут строится заново от сохранённой позиции курьера. Ошибка Update Courier Location пишется в лог, движение продолжается.

Simulator читает точки доставки из своей выборки работы (`slices/simulator`): выборка `assigned`/`picked_up` получает `pickup_point` и `dropoff_point`. Доставки без точек Simulator не двигает.

## UI

Компонент `CourierMap` в `features/view-order`:

- показывается, когда `order.tracking` не `null` (статусы `COURIER_ASSIGNED` и `PICKED_UP`);
- SVG `viewBox` по сетке Virtual City: линии улиц, кварталы, маркеры ресторана (pickup) и клиента (dropoff), маркер курьера и полилиния следа из полученных точек;
- координаты переводятся в `viewBox` по параметрам `city` из ответа; карта не знает констант сервера;
- подписка `courierMoved(channel)` стартует после query; при каждом (пере)подключении query перезапрашивается, след начинается с `courierPosition` (ADR 36);
- маркер интерполируется от прежней точки к новой за интервал между их `recordedAt` через `requestAnimationFrame`; таймеров запроса данных нет (ADR 36 запрещает polling);
- при `prefers-reduced-motion` маркер переносится без анимации;
- карта — `role="img"` с текстовым `aria-label` («Курьер едет в ресторан» или «Курьер везёт заказ»), смена фазы объявляется уже существующим `aria-live`.

Моки: `mocks/subscriptions.ts` эмулирует `courierMoved` на mock store так же, как `orderChanged`.

## ops

- Valkey `delivery` в `nomnom-data` без persistence: ACL-пользователь `delivery` (`PUBLISH` на `&delivery.tracks.*`), `bff` (`allchannels`, только подписка). Экспортера Alloy нет: ключей и кеш-метрик у pub/sub нет. URL — Secrets `delivery-valkey` и `bff-tracks-valkey`.
- NetworkPolicy: egress `delivery` и `bff` к Valkey `delivery`.

## Риски

- Чтение активной доставки на каждой точке — запрос в PostgreSQL на 1 Hz от каждого движущегося курьера. Для демо-стенда это немного; при росте нагрузки канал курьера можно держать в памяти процесса или Valkey.
- Router читает канал без буфера: точки, опубликованные до подписки или во время разрыва, теряются. Это нормально, потому что важна только последняя позиция, а её отдаёт query.
- Сохранённая позиция отстаёт до `TRACKING__PERSIST_INTERVAL`, и маркер после перезагрузки страницы может прыгнуть назад до первой точки потока.
