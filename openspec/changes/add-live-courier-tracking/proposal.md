# Proposal

## Why

После назначения курьера экран заказа показывает только статус, имя курьера и сроки. Покупатель не видит, где курьер и как он движется. `delivery` уже хранит последнюю позицию курьера (`UpdateCourierLocation`), но позиция не доходит до клиента. Demo Simulator не двигает курьеров: change `add-demo-simulator` явно исключил «имитацию движения по карте».

Настоящие карты (OSM-тайлы, геокодинг, маршрутизация) для демо-стенда тяжёлые, а адреса pickup и dropoff из проекта удалены (`CreateDeliveryRequest` резервирует их поля). Нужна лёгкая модель пространства, по которой Simulator может водить курьера, а UI — рисовать его движение.

## What Changes

- **Virtual City.** Город — квадратная сетка улиц внутри рамки широты и долготы, параметры задаёт конфиг `delivery` (`TRACKING__CITY_*`). Координаты остаются широтой и долготой, поэтому контракт `UpdateCourierLocation` не меняется, и позже можно подложить настоящую карту.
- **Виртуальные точки доставки.** Create Delivery выбирает случайные перекрёстки Virtual City для pickup и dropoff и сохраняет их в `Delivery`. Это демо-замена отсутствующим адресам.
- **Tracking Channel.** У `Delivery` есть случайный непредсказуемый токен канала. Его выдаёт только query с проверкой `order#view`, и он нужен, чтобы подписаться на поток позиций.
- **Поток позиций без Kafka.** Update Courier Location публикует точку в Valkey pub/sub, в канал активной доставки курьера. В PostgreSQL позиция записывается с прореживанием: не чаще `TRACKING__PERSIST_INTERVAL`. Integration Event на точку не публикуется.
- **BFF.** У `Order` появляется `tracking`: канал, параметры Virtual City, точки pickup и dropoff, последняя сохранённая позиция. Подписка `courierMoved(channel)` идёт через провайдер Redis в Cosmo Streams по SSE. Координаты едут в самом событии (все поля `CourierPosition` — ключ сущности), без запроса за данными на каждую точку.
- **Simulator водит курьера.** Когда курьер принимает offer, Simulator строит случайный кратчайший путь по улицам: от курьера к pickup, после Pickup — от pickup к dropoff. Скорость подбирается так, чтобы курьер приехал к уже запланированным `expected_pickup_at` и `expected_delivered_at`. Каждые `SIMULATOR__TRACK_INTERVAL` Simulator вызывает настоящий Update Courier Location от имени курьера.
- **UI.** На странице заказа в статусах `COURIER_ASSIGNED` и `PICKED_UP` показывается SVG-карта сетки с рестораном, клиентом, курьером и его следом. Между точками маркер движется плавно через `requestAnimationFrame`, без polling.

Не входит:

- настоящие карты, адреса, геокодинг и маршрутизация;
- отслеживание на flow board и в Courier Pool;
- ETA по фактическому движению: сроки остаются теми, что назвал курьер;
- история трека и retention позиций;
- Custom Module или собственная сборка Router.

## Capabilities

### New Capabilities

- `courier-live-tracking`: Virtual City, виртуальные точки доставки, Tracking Channel, поток позиций, движение курьера в Simulator.

### Modified Capabilities

- `courier-availability`: Update Courier Location публикует точку и записывает позицию с прореживанием.
- `customer-graphql-contract`: `Order.tracking` и подписка `courierMoved`.
- `order-tracking-ui`: карта движения курьера на экране заказа.

## Impact

- **`boundaries/delivery`:**
  - слайс `delivery`: виртуальные точки и Tracking Channel в `Delivery`, миграция, query Get Order Tracking (`order#view`);
  - слайс `courier`: публикация точки и прореживание записи в Update Courier Location;
  - слайс `simulator` и `di::SimulatorWorker`: маршрут по сетке и тики позиций;
  - Valkey-адаптер публикации, конфиг `TRACKING__*`, `VALKEY__URL`, `SIMULATOR__TRACK_INTERVAL`;
  - proto `service.proto` (`GetOrderTracking`).
- **`boundaries/bff`:** клиент `delivery`, `Order.tracking`, events subgraph `courierMoved` с провайдером Redis, `config.yaml` и `config.production.yaml`, composed client schema.
- **`boundaries/ui`:** карта на экране заказа, подписка, i18n, моки (`mocks/subscriptions.ts`), тесты.
- **ops:** Valkey `delivery` в `nomnom-data` с ACL-пользователями `delivery` (publish) и `bff` (subscribe), Secrets, NetworkPolicy egress `delivery` и `bff`, values kind, Helm `valkey.urlSecret` (`delivery`) и `tracks.redisUrlSecret` (`bff`).
- **Документация:** ADR 36 (второй вид подписок: данные в событии, канал-капабилити), ADR 43 (Valkey `delivery` как pub/sub, не кеш), README `delivery`, `bff`, `ui`, `docs/ubiquitous-language.md`.
