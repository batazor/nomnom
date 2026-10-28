# Proposal

## Why

Сейчас заказ готовится «как можно скорее»: кухня ставит его в очередь сразу после Acceptance, курьер ищется сразу после Acceptance. Покупатель не может заказать «к 19:00». Для этого нужен обратный расчёт: когда кухне начать и когда звать курьера, чтобы заказ приехал в выбранное окно. Это первый процесс `orders` с бизнес-таймерами на часы вперёд и первый случай ограничения приёма заказов по ёмкости.

## What Changes

- **Окна доставки.** Ресторан задаёт настройки запланированных заказов (`SetScheduledOrderSettings`, `restaurant#manage`): ёмкость на окно, отступ первого окна от открытия и допуск после закрытия. Нет настроек — нет запланированных заказов. Окно допустимо, только если его начало внутри интервала работы ресторана: не раньше открытия + отступ и не позже закрытия + допуск. Окна — сетка шириной `SCHEDULED_SLOT__WIDTH` от `now + SCHEDULED_SLOT__MIN_LEAD` до `now + SCHEDULED_SLOT__MAX_HORIZON`; `ListScheduledSlots` отдаёт окна с признаком доступности.
- **Резервация окна.** Place Order с `delivery_window_start` синхронно резервирует окно в `restaurants` (hold с `SCHEDULED_SLOT__HOLD_TTL`). Окно занято — отказ `SLOT_UNAVAILABLE` с ближайшими свободными окнами. Workflow подтверждает резервацию после оплаты и освобождает её компенсацией.
- **Оценки.** `restaurants` отдаёт базовое время приготовления (медиана Wait Time Statistics). `delivery` получает новую оценку длительности доставки по собственным фактам: время от Create Delivery до Pickup и от Pickup до вручения. Адресов у Delivery нет, поэтому оценка общая.
- **Обратный расчёт и таймеры.** Workflow считает `planned_ready_at = window_start − transit`, `start_at = planned_ready_at − preparation − SCHEDULED_ORDER__PREPARATION_BUFFER`, `courier_request_at = planned_ready_at − courier_lead`. Перед стартом одна контрольная точка `start_at − SCHEDULED_ORDER__REPLAN_LEAD` пересчитывает `start_at` и `courier_request_at` по свежим оценкам. Таймеры — Temporal timers, без polling.
- **Кухня.** Restaurant Order запланированного заказа приходит с окном и `planned_ready_at`. Кухня принимает его сразу: заказ остаётся в `ACCEPTED` (колонка «Запланированные»), а в `QUEUED` его переводит workflow в `start_at` через gRPC `QueueRestaurantOrder`. Новый state не вводится.
- **Очередь кухни.** Поставленный в очередь запланированный заказ берётся первым. Порядок Kitchen Queue: запланированные (премиум раньше, затем по `planned_ready_at`) → премиум и состарившиеся обычные → остальные обычные.
- **Статистика ожидания** считает длительности от `queued_at`, а не от `accepted_at`, и не учитывает заказы в `ACCEPTED`: иначе запланированные заказы исказили бы её на часы.
- **Отмена** запланированного заказа доступна покупателю до `window_start − CANCELLATION__SCHEDULED_CUTOFF`.
- **Оплата** не меняется: Authorization сразу при оформлении. `SCHEDULED_SLOT__MAX_HORIZON` MUST быть меньше срока жизни hold у provider, поэтому повторная Authorization не нужна.
- **Часовой пояс Market.** `CreateMarket` принимает обязательный IANA `time_zone`, `GET /markets` отдаёт `timeZone`. Сервер считает только в UTC-моментах; UI показывает окна и сроки во времени Market, а в день перевода часов различает повторяющиеся окна смещением.
- **UI.** Выбор окна при оформлении, статус «Запланирован» и окно на экране заказа, колонка «Запланированные» на флоу-борде.

Не входит:

- перенос окна и изменение состава запланированного заказа — только отмена;
- адресная оценка доставки и базовое время по часам суток;
- ограничение приёма обычных ASAP-заказов: оно остаётся без ограничений, как в Wait Time Statistics;
- пояс у ресторана или клиента: они всегда в поясе своего Market.

## Capabilities

### New Capabilities

- `scheduled-order-slots`: ёмкость окон ресторана, список окон, резервация, подтверждение и освобождение.
- `scheduled-order-planning`: обратный расчёт, таймеры старта и вызова курьера, контрольная точка пересчёта.
- `delivery-duration-estimate`: оценка `courier_lead` и `transit` по фактам доставки.

### Modified Capabilities

- `order-use-cases`: Place Order с окном доставки, окно отмены запланированного заказа.
- `order-fulfillment-orchestration`: маршрут запланированного заказа, отсчёт deadline Preparation, компенсация резервации.
- `restaurant-order-lifecycle`: Receive Order Request с окном, Accept без постановки в очередь, Queue Restaurant Order по gRPC.
- `kitchen-board`: List Scheduled Orders, класс запланированных в порядке Kitchen Queue.
- `kitchen-wait-time`: длительности от `queued_at`.
- `customer-graphql-contract`: окна, `PlaceOrderInput.deliveryWindowStart`, `Order.deliveryWindow`, статус `SCHEDULED`.
- `customer-web-application`: выбор окна при оформлении.
- `order-tracking-ui`: запланированный заказ на экране заказа.
- `order-flow-board`: колонка «Запланированные».
- `cell-routing`: часовой пояс Market в реестре и в `GET /markets`.

## Impact

- **`boundaries/restaurants`:**
  - слайс `restaurant`: настройки запланированных заказов, `SetScheduledOrderSettings`, базовое время приготовления в представлении ресторана;
  - новый слайс резервации окон: aggregate `ScheduleSlot`, коллекция MongoDB, gRPC `ListScheduledSlots`, `ReserveScheduledSlot`, `ConfirmScheduledSlot`, `ReleaseScheduledSlot`;
  - слайс `restaurantorder`: окно и `planned_ready_at` в Restaurant Order, класс запланированных в правиле очереди, Accept для запланированного заказа, gRPC `QueueRestaurantOrder`, `ListScheduledOrders`, статистика от `queued_at`;
  - симулятор кухни; конфиг `SCHEDULED_SLOT__*`.
- **`boundaries/orders`:**
  - Order: окно доставки; Place Order с резервацией; cancellation policy с `CANCELLATION__SCHEDULED_CUTOFF`; migration PostgreSQL;
  - use case расчёта плана; workflow: ветка запланированного заказа, stage `scheduled`, таймеры, Activities `ConfirmSlotReservation`, `PlanSchedule`, `QueueRestaurantOrder`, компенсация `ReleaseSlotReservation`;
  - конфиг `SCHEDULED_ORDER__*`.
- **`boundaries/delivery`:** статистика длительностей по фактам Delivery, gRPC `EstimateDeliveryDuration`, конфиг `DELIVERY_ESTIMATE__*`.
- **`boundaries/controlplane`:** `time_zone` в `CreateMarket` и реестре, migration для существующих Market, `timeZone` в ConfigMap `GET /markets`.
- **`boundaries/bff`, `boundaries/ui`:** схема, resolvers, оформление, экран заказа, флоу-борд, i18n, моки.
- **`ops`:** values трёх сервисов, GRPCRoute новых методов, seed ёмкости для демо-ресторанов, kind e2e запланированного заказа.
- **Документация:** правки [ADR 5 orders](../../../boundaries/orders/docs/adr/0005-order-fulfillment-orchestration.md) (таймеры запланированного заказа, stage `scheduled`, компенсация резервации) и [ADR 0002 restaurants](../../../boundaries/restaurants/docs/adr/0002-mongodb-collections-and-document-boundaries.md) (коллекция окон), [ADR 41](../../../docs/adr/0041-cell-based-architecture-with-control-plane.md) (пояс Market); `docs/kitchen.md`, `docs/fsm.md`, `docs/ubiquitous-language.md`, README сервисов. Новый ADR не создаётся.

Зависит от `add-restaurant-opening-hours` (расписание и пояс ресторана) и от архивации `add-kitchen-wait-time` (базовое время, FR-644) и `add-premium-priority` (`priority`, правило очереди FR-241).
