# Proposal

## Why

Сейчас в demo-стенде один курьер — сам пользователь, а поваров нет: команды кухни выполняет owner или staff, и Simulator кухни планирует шаги каждого принятого заказа независимо, то есть кухня готовит сколько угодно заказов одновременно. Kitchen Capacity в [kitchen.md](../../../docs/kitchen.md#kitchen-capacity) и в [Ubiquitous Language](../../../docs/ubiquitous-language.md) остаётся без определения. Поэтому Kitchen Queue не копится, нагрузку видно только по дашбордам, а исполнителей заказа нельзя назвать по имени.

## What Changes

- **Cook Pool** (`restaurants`, свой у каждого ресторана): Cook с именем; Hire Cook, Put Cook On Line, Take Cook Off Line, Retire Cook. Start Preparation назначает заказу свободного Cook на линии; если свободного нет — бизнес-отказ `NoCookAvailable`, заказ остаётся в `QUEUED`. Cook занят, пока его заказ в `PREPARING` или `ASSEMBLED`, и освобождается на `READY`, `FAILED` или `CANCELLED`. При пустой линии заказы принимаются и ждут в очереди; Acceptance от линии не зависит.
- **Courier Pool** (`delivery`, общий на платформу): у `Courier` появляется обязательное `name`, задаваемое в Register Courier; Retire Courier выводит курьера вне линии из пула. Поставить на линию и снять с линии — существующий Set Courier Availability, теперь доступный и диспетчеру (`platform#dispatcher`).
- **Pool Load**: query пула возвращает размер, число на линии, число занятых и backlog (Kitchen Queue для поваров, Delivery в `Waiting` для курьеров) и состав пула со статусом каждого.
- **Simulator**: кухня начинает Preparation только при свободном Cook и планирует оставшиеся шаги от фактического старта; seed добавляет именованных поваров каждому ресторану и именованных курьеров с позициями.
- **BFF и UI**: в шапках колонок кухни и курьеров flow-board — пул (линия, загрузка, backlog), список работников с действиями «на линию», «снять», «убрать», форма «+ Повар» и «+ Курьер»; имена на карточках заказов («Готовит», «Везёт»); имя курьера в отслеживании заказа покупателя. Обновления — только subscriptions (ADR 36).

Не входит: ручное назначение повара (перетаскивание заказа на повара; курьеру диспетчер перетаскиванием только предлагает заказ — Offer Delivery To Courier в `delivery`, курьер принимает offer сам), расчёт `expected_ready_at` по очереди и числу поваров на линии, Kitchen Station, смены по расписанию, возврат выбывшего работника в пул.

## Capabilities

### New Capabilities

- `cook-pool`.
- `courier-pool`.
- `staff-pool-board`.

### Modified Capabilities

- `courier-availability` — Register Courier с именем.
- `courier-command-api` — `RegisterCourier` требует `platform#manage_couriers`.
- `restaurant-order-lifecycle` — Start Preparation назначает Cook.

## Impact

`authorization/schema.zed` и `validation.yaml` (`restaurant#manage_cooks`, `platform#dispatcher`, `platform#manage_couriers`), `ops/kind/authz/bootstrap-kind.yaml`; proto `restaurants` и `delivery` и их generated-копии в BFF; новый слайс `cook` и изменения `restaurantorder`, `simulator` в `boundaries/restaurants`, коллекция `cooks` и поле `cookId` в `restaurant_orders` ([ADR 0002 restaurants](../../../boundaries/restaurants/docs/adr/0002-mongodb-collections-and-document-boundaries.md)); миграция `couriers` и use cases слайса `courier` в `boundaries/delivery`; GRPCRoute новых RPC в `ops/kind/manifests/gateway/gateway.yaml`; схема и слайсы BFF; flow-board и экран заказа UI; `ops/kind/demo/files/seed.json` и `seed.py`; [kitchen.md](../../../docs/kitchen.md), [Ubiquitous Language](../../../docs/ubiquitous-language.md).
