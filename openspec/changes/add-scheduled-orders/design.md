# Design

## Термины

- **Scheduled Order** — Order с окном доставки. Order без окна — обычный ASAP-заказ, его маршрут не меняется.
- **Delivery Window** — полуинтервал `[window_start, window_end)`, в который покупатель ждёт заказ. `window_end = window_start + SCHEDULED_SLOT__WIDTH`.
- **Schedule Slot** — Delivery Window ресторана с ёмкостью и резервациями.
- **Slot Reservation** — место заказа в Schedule Slot: `held` до подтверждения, затем `confirmed`.
- **Planned Ready At** — момент, к которому кухня должна приготовить запланированный заказ.

## Время и часовой пояс

Все моменты — UTC instants: окна, таймеры и арифметика плана считаются только в моментах, никогда в «настенном» времени. Клиент и ресторан всегда находятся в одном Market, поэтому пояс принадлежит Market:

- `CreateMarket` принимает обязательный IANA `time_zone` (`Europe/Berlin`); неизвестный пояс — `INVALID_ARGUMENT`. Существующим Market migration реестра задаёт пояс явно (`berlin` — `Europe/Berlin`): это факт, а не значение по умолчанию;
- `GET /markets` отдаёт `timeZone`; UI форматирует окна и сроки заказа в поясе Market, а не в поясе браузера.

Перевод часов:

- сетка окон выровнена по UTC кратно `SCHEDULED_SLOT__WIDTH`, а ширина MUST делить 30 минут. Тогда границы окон в местном времени кратны ширине при любом смещении, кратном 30 минутам, в том числе до и после перевода;
- в пропущенный час окон в местном времени просто нет: их моменты отображаются уже по новому смещению;
- повторяющийся час даёт два разных окна с одинаковыми местными подписями. UI в такой день показывает у них смещение (`01:00 GMT+2`, `01:00 GMT+1`), выбор всё равно передаётся моментом, поэтому путаницы на сервере нет;
- длительности (оценки, буфер, lead) не зависят от перевода, потому что вычитаются из моментов.

## Ёмкость и окна (`restaurants`)

- `Restaurant.scheduledOrders?` — настройки запланированных заказов, задаются вместе и только вместе:
  - `slotCapacity: int > 0` — сколько запланированных заказов ресторан берёт на одно окно;
  - `openingLead: duration ≥ 0` — первое окно интервала начинается не раньше `opens_at + openingLead`, чтобы кухне не пришлось начинать до открытия;
  - `closingGrace: duration ≥ 0` — последнее окно интервала начинается не позже `closes_at + closingGrace`.

  Задаются и убираются `SetScheduledOrderSettings(restaurant_id, settings | clear)` с `restaurant#manage`. Отдельный RPC, а не поле `UpdateRestaurant`: это настройки планирования, а не карточка ресторана. Нет настроек — ресторан не берёт запланированные заказы. Это пустое состояние, а не подмена.
- Интервалы работы и пояс ресторана — из change `add-restaurant-opening-hours`. Окно допустимо, если существует интервал, для которого `opens_at + openingLead ≤ window_start ≤ closes_at + closingGrace`. Между интервалами (обед, ночь) окон нет.
- Обязательный конфиг `restaurants`: `SCHEDULED_SLOT__WIDTH`, `SCHEDULED_SLOT__MIN_LEAD`, `SCHEDULED_SLOT__MAX_HORIZON`, `SCHEDULED_SLOT__HOLD_TTL`, `SCHEDULED_SLOT__ALTERNATIVES`. Пустое или невалидное значение, ширина, не делящая 30 минут, и `MIN_LEAD ≥ MAX_HORIZON` — падение на старте. `MAX_HORIZON` MUST быть меньше срока жизни Authorization hold у provider; это ограничение записывается рядом со значением в values.
- **List Scheduled Slots**(`restaurant_id`) — окна от первой границы сетки не раньше `now + MIN_LEAD` до `now + MAX_HORIZON`, только допустимые по расписанию, каждое с `available`. Окно недоступно, если активных резерваций не меньше ёмкости. Ресторан без настроек или не принимающий заказы сейчас — пустой список: кухня принимает запланированный заказ сразу, поэтому оформить его можно, только пока ресторан работает. Query публичный, как Get Restaurant.
- Aggregate `ScheduleSlot` (`restaurantId`, `windowStart`) — документ на окно с резервациями и `version`. Активная резервация — `confirmed` или `held` с `expiresAt > now`. Просроченные `held` вычищаются при следующей записи этого окна: заказ переносится в список `lapsed` окна, чтобы Confirm нашёл своё окно и заново проверил ёмкость. TTL-индекс по `windowEnd` убирает документы прошедших окон. Фоновых заданий нет.
- **Reserve Scheduled Slot**(`restaurant_id`, `window_start`, `order_id`) — от `service` identity `orders`:
  - окно не на сетке, раньше `now + MIN_LEAD`, позже горизонта или вне расписания — `INVALID_ARGUMENT`;
  - ресторан не принимает заказы сейчас или без настроек — `FAILED_PRECONDITION` `SCHEDULED_ORDERS_UNAVAILABLE`;
  - мест нет — `FAILED_PRECONDITION` `SLOT_UNAVAILABLE` и до `ALTERNATIVES` ближайших доступных окон в деталях ошибки;
  - иначе резервация `held` с `expiresAt = now + HOLD_TTL`; ответ — окно (`window_start`, `window_end`);
  - повтор с тем же `order_id` возвращает ту же резервацию.

  Конкурентные резервации одного окна дают write conflict по `version`; use case повторяет попытку, и ёмкость не превышается.
- **Confirm Scheduled Slot**(`order_id`) — `held` → `confirmed`, повтор — no-op. Если hold просрочен и вычищен, подтверждение заново проверяет ёмкость: есть место — `confirmed`, нет — `SLOT_UNAVAILABLE`.
- **Release Scheduled Slot**(`order_id`) — удаляет резервацию; отсутствующая резервация — успешный no-op.
- Изменение настроек или расписания не трогает существующие резервации, а новые проверяются по новым значениям.

## Place Order (`orders`)

- `PlaceOrderRequest.delivery_window_start` — необязательный момент. Задан — заказ запланированный.
- `priority = PREMIUM` допустим и с окном: доплата берётся так же, а заказ идёт первым среди запланированных (см. «Kitchen Queue»).
- Порядок: проверка состава → catalog snapshot → запись relationship (FR-102) → `ReserveScheduledSlot` с заранее сгенерированным `order_id` → транзакция `Order` + Outbox. Order хранит `delivery_window` из ответа резервации, `OrderPlaced` несёт его.
- Ошибки резервации проходят в ответ Place Order с теми же reason (`SLOT_UNAVAILABLE` с альтернативами, `SCHEDULED_ORDERS_UNAVAILABLE`, `INVALID_ARGUMENT`); Order не сохраняется.
- Если транзакция не состоялась, hold не освобождается явно, а истекает через `HOLD_TTL`. Повтор Place Order с тем же idempotency key возвращает сохранённый Order (FR-101) без новой резервации.

## Оценки

- **Приготовление** — базовое время ресторана (медиана Wait Time Statistics, до набора выборки — `KITCHEN__PREPARATION_TIME`) в представлении ресторана: `Restaurant.baseline_preparation` в `restaurants.proto`. Current Wait не используется: он описывает кухню сейчас, а не в момент окна.
- **Доставка** (`delivery`): статистика по собственным фактам Delivery — последние `DELIVERY_ESTIMATE__SAMPLE_SIZE` длительностей:
  - `courier_lead` — Create Delivery → Pickup;
  - `transit` — Pickup → вручение.

  Оценка — медиана; пока выборка не набрана, используются обязательные `DELIVERY_ESTIMATE__BASELINE_COURIER_LEAD` и `DELIVERY_ESTIMATE__BASELINE_TRANSIT`. Статистика общая по Market: у Delivery нет адресов. gRPC `EstimateDeliveryDuration(market_id)` от `service` identity `orders`. Ведёт её consumer собственных `CourierAssignedToDelivery`/`DeliveryPickedUp`/`DeliveryCompleted` по образцу Wait Time Statistics; отменённые доставки выбывают без длительности.

## План (`orders`)

Расчёт — чистая domain-политика слайса `order` (FR-483). Use case Plan Schedule читает оценки и возвращает план. Workflow вызывает его через Activity `PlanSchedule` и хранит план в Event History. В `Order` план не пишется: покупателю виден только Delivery Window и `expected_ready_at`.

```text
planned_ready_at   = window_start − transit
start_at           = planned_ready_at − preparation − SCHEDULED_ORDER__PREPARATION_BUFFER
courier_request_at = planned_ready_at − courier_lead
replan_at          = start_at − SCHEDULED_ORDER__REPLAN_LEAD
```

- `planned_ready_at` вычисляется один раз в первом `PlanSchedule` и дальше не меняется: это срок, который увидела кухня. Перевозка, сократившаяся или выросшая после этого, сдвигает вручение внутри окна, а ширина окна её поглощает.
- В `replan_at` Activity `PlanSchedule` вызывается повторно с тем же `planned_ready_at` и даёт новые `start_at` и `courier_request_at` по свежим оценкам. Контрольная точка одна; подписки на изменения загрузки и polling не вводятся.
- Момент, оказавшийся в прошлом, означает «сейчас»: таймер не ставится, шаг выполняется сразу.
- `SCHEDULED_ORDER__PREPARATION_BUFFER` и `SCHEDULED_ORDER__REPLAN_LEAD` — обязательный конфиг; пустой или неположительный — падение на старте.

## Маршрут workflow запланированного заказа

```text
AuthorizePayment → RecordPaymentAuthorization → ConfirmSlotReservation → PlanSchedule
→ RequestRestaurantOrder(window, planned_ready_at) → ждать RestaurantOrderAccepted
→ stage scheduled: timer replan_at → PlanSchedule
                   timer start_at → QueueRestaurantOrder
                   timer courier_request_at → CreateDelivery
→ дальше обычный маршрут: Assignment, Preparation, Pickup, Capture, Delivery Completion
```

- `ConfirmSlotReservation` отвечает `SLOT_UNAVAILABLE`, только если hold истёк и окно заполнилось. Тогда workflow отменяет Authorization и завершает Order кодом `slot_unavailable`.
- Ветка выбирается по наличию окна во входе workflow (из `OrderPlaced`). ASAP-маршрут не меняется.
- Stage `scheduled` публикуется Search Attribute от `RestaurantOrderAccepted` до `QueueRestaurantOrder`.
- Deadline Acceptance считается как обычно: кухня подтверждает запланированный заказ сразу, а не к окну. Deadline Preparation отсчитывается от `QueueRestaurantOrder`, а не от Acceptance. Deadline Assignment отсчитывается от `CreateDelivery`.
- `QueueRestaurantOrder` — Activity с ключом `order_id + queue`, вызывает gRPC `restaurants` от `service` identity worker'а.
- Компенсации: журнал Saga получает шаг резервации. При отказе оплаты, отмене или неудаче после резервации workflow вызывает `ReleaseSlotReservation` (ключ `order_id + release_slot`). После Pickup резервация не освобождается: окно своё отработало.
- Workflow запланированного заказа живёт до `MAX_HORIZON` и переживает выкатки worker'а. Поэтому ветка вводится под Temporal versioning (patch), а replay-тесты на histories в stage `scheduled` обязательны.

## Kitchen Queue

Правило очереди из `add-premium-priority` (FR-241) получает класс выше премиума:

1. **запланированные** в `QUEUED`: сначала `PREMIUM`, затем `STANDARD`; внутри — по `planned_ready_at`, затем `queued_at` и id. У запланированного заказа жёсткий срок к окну, поэтому он обгоняет и премиум, и состарившиеся обычные;
2. премиум и обычные, ждущие дольше базового времени × `KITCHEN_QUEUE__AGING_FACTOR`;
3. остальные обычные; внутри классов 2 и 3 — FIFO по `queued_at`, затем по id.

Start Next Preparation берёт голову по этому правилу без изменений в самой команде.

## Restaurant Order

- `ReceiveOrderRequest` получает необязательный блок `schedule { window_start, window_end, planned_ready_at }`. Restaurant Order хранит его как данные, а не как State.
- **Accept** запланированного заказа: `RECEIVED` → `ACCEPTED` с `accepted_at` и `expected_ready_at = planned_ready_at`, без `queued_at`; Outbox — `RestaurantOrderAccepted`. Срок от кухни (`expected_ready_at` в запросе) для запланированного заказа — `INVALID_ARGUMENT`: срок уже задан окном.
- **Queue Restaurant Order** (FR-224) публикуется по gRPC как `QueueRestaurantOrder(restaurant_order_id)` для `service` identity `orders`. `expected_ready_at` не меняется; если кухня не успевает, она сообщает задержку через Report Preparation Delay, как сейчас.
- **List Scheduled Orders**(`restaurant_id`) — заказы в `ACCEPTED` с окном, по `planned_ready_at`; `restaurant#operate_kitchen`.
- Cancel Restaurant Order уже допускает `ACCEPTED` (FR-230).
- Wait Time Statistics: длительность — `queued_at` → готов, незавершённые — заказы с `queued_at`. Для ASAP-заказов `accepted_at = queued_at` (одна UoW), поэтому их статистика не меняется. Expected Ready At ASAP по-прежнему — момент Acceptance плюс текущее ожидание.
- Consumer статистики читает только Integration Events, а Queue Restaurant Order факта не публикует. Поэтому момент постановки в очередь несут уже опубликованные факты, поля добавляются в `events.proto` только аддитивно:
  - `RestaurantOrderAccepted.queued_at` задан, только если заказ встал в очередь в той же UoW (заказ без окна). Такой факт открывает заказ в статистике с этим моментом; факт без `queued_at` — принятый запланированный заказ — статистику не меняет.
  - `PreparationStarted` получает `restaurant_id` и `queued_at`. Он открывает запланированный заказ с его `queued_at`; для уже открытого заказа это no-op. Запланированный заказ идёт первым в очереди, поэтому между постановкой и стартом он почти не ждёт, и текущее ожидание он не занижает.
  - `RestaurantOrderReady` записывает длительность от `queued_at` открытого заказа, отмена и сбой убирают его без длительности. Поле `queued_at` в Ready не нужно: момент уже в незавершённых.
  - Хранимые незавершённые заказы migration переименовывает `acceptedAt` → `queuedAt`: до этого change все заказы вставали в очередь в UoW Acceptance. ASAP-факт, опубликованный до выкатки без `queued_at`, откроет `PreparationStarted` нового формата.

## Отмена

Cancellation policy (FR-462) для запланированного заказа открывает окно отмены при Place Order и закрывает его в `window_start − CANCELLATION__SCHEDULED_CUTOFF`. Рубеж считается из данных `Order`, а не из плана workflow, поэтому Get Order показывает его без Temporal, а покупатель видит понятное «отмена до 18:00». Обязательный конфиг, неположительный — падение на старте. Update `CancellationRequested` в stage `scheduled` ведёт к `CancelRestaurantOrder`, `ReleaseSlotReservation`, `CancelPaymentAuthorization`.

## Customer API и UI

- GraphQL:
  - `Restaurant.scheduledSlots: [DeliveryWindow!]!` (`start`, `end`, `available`);
  - `PlaceOrderInput.deliveryWindowStart`;
  - `Order.deliveryWindow`;
  - `OrderStatus.SCHEDULED` — подтверждённый запланированный заказ, у которого ещё нет факта Preparation Started;
  - problems `SLOT_UNAVAILABLE` (с `alternativeWindows`) и `SCHEDULED_ORDERS_UNAVAILABLE`.
- Оформление: переключатель «Как можно скорее / Ко времени». Если у ресторана нет окон, переключателя нет. Окна сгруппированы по дням в поясе Market. Чекбокс приоритета доступен и для запланированного заказа. При `SLOT_UNAVAILABLE` UI предлагает альтернативные окна, корзина сохраняется.
- Экран заказа: статус «Запланирован», окно в шапке; бар готовки появляется только после старта Preparation.
- Флоу-борд: колонка «Запланированные» между входящими и очередью, карточка показывает окно и `planned_ready_at`, действий у карточки нет. Симулятор кухни принимает запланированные заказы без собственного срока.
- Обновления только subscriptions (ADR 36), без polling.

## Риски

- Workflow живут часами: versioning и replay становятся обязательными с этим change, а в kind перед выкаткой worker'а незавершённые workflow прерываются, как в прошлых change.
- Оценка доставки без адресов грубая; окно её поглощает только при ширине окна больше разброса перевозки.
- Ресторан, закрытый вручную к моменту старта, получит заказ в очередь; если его никто не возьмёт, обычный deadline Preparation отменит заказ с освобождением резервации и Authorization. Окна по расписанию это исключают для штатного закрытия.
- Если запланированных заказов на кухне много, обычные ждут дольше: ёмкость на окно и есть рычаг ресторана против этого.
- Резервация — первое ограничение приёма. Оно касается только запланированных заказов, а позиция «приём не ограничивается» в `docs/kitchen.md` уточняется для ASAP.
