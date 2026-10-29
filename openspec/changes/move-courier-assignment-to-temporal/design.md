# Design

## Термины

- **Assignment Workflow** — Temporal workflow `delivery:<delivery_id>`, который ведёт одну `Delivery` от создания до Pickup или отмены: ищет курьера, держит срок offer, возвращает доставку в поиск после Reassign Courier.
- **Wake** — signal, который будит Assignment Workflow. Он не несёт решения: workflow после него заново читает состояние доставки Activity. Источник истины — PostgreSQL `delivery`, а не Event History.
- **Courier Candidate** — available курьер с сохранённой позицией: только такого Find Courier может выбрать (FR-307, FR-542).

## Workflow

Workflow `delivery:<delivery_id>` (task queue `delivery`, namespace из `TEMPORAL__NAMESPACE`). Состояние доставки workflow не хранит: каждый шаг — Activity над use case, решение принимает use case по сохранённому состоянию.

```text
start
  └─ loop
       Activity AdvanceAssignment(delivery_id) → один из исходов:
         offered(offer_id, expires_at) → ждать Wake или timer(expires_at)
                                           timer → Activity ExpireDeliveryOffer(offer_id) → loop
                                           Wake  → loop
         no_candidate                   → ждать Wake → loop
         assigned                       → ждать Wake → loop
         finished (picked up / cancelled / failed / delivered) → завершить workflow
```

`AdvanceAssignment` — Activity над новым use case Advance Assignment (вместо Dispatch Deliveries для одной доставки):

- `Delivery` закрыта, отменена или забрана — `finished`;
- `Delivery` назначена — `assigned`;
- `Delivery` ждёт и есть pending непросроченный offer — `offered` с его `expires_at`;
- `Delivery` ждёт без pending offer — Find Courier и Offer Delivery в одной Unit of Work, как сейчас Dispatch Deliveries для одной доставки: `offered` или `no_candidate`;
- конкурентное изменение (optimistic concurrency) — повтор Activity по retry policy.

Pending offer, у которого `expires_at` уже прошёл, use case сам закрывает как `expired` в той же Unit of Work и ищет следующего курьера: workflow, пробуждённый после срока, не ждёт ещё один таймер.

`ExpireDeliveryOffer` — Activity над Expire Delivery Offer для одного offer: `AlreadyApplied` и offer, уже принятый, отклонённый или отозванный, — успех без изменений.

Signal один: `Wake` с полем `reason` (`offer_answered`, `courier_available`, `courier_unassigned`, `delivery_closed`) — только для диагностики, на ход workflow не влияет. Дедупликации не нужно: лишний Wake приводит к ещё одному чтению состояния.

Event History растёт с каждой итерацией. После `ASSIGNMENT_WORKFLOW__CONTINUE_AS_NEW_AFTER` итераций workflow делает continue-as-new: состояния у него нет, поэтому переносить нечего.

## Запуск и signals после commit

Temporal не входит в Unit of Work (`docs/unit-of-work.md`), поэтому start и signal выполняются после commit, через порт `AssignmentProcess` в `applications/` с адаптером в `infrastructure/temporal`.

- **Create Delivery** после commit запускает workflow с политикой `WorkflowIdConflictPolicy::UseExisting`, и на первом вызове, и на повторе, который возвращает уже созданную `Delivery`. Ошибка запуска — `UNAVAILABLE`. Create Delivery вызывается Activity `orders`, и её retry повторяет вызов: потеря запуска закрывается без Outbox-relay.
- **Accept, Reject Delivery Offer, Reassign Courier, Cancel Delivery, Confirm Pickup** после commit передают Wake своей доставки.
- **Ошибка signal — `UNAVAILABLE`.** Повтор вызова, который уже не меняет состояние (`AlreadyApplied`, повтор того же ответа), всё равно передаёт Wake. Для Accept и Reject потеря signal не блокирует процесс: таймер offer всё равно сработает. Для остальных повтор вызова обязателен, иначе workflow будет ждать.
- Wake закрытому workflow (доставка уже завершила назначение) — не ошибка.

## Пробуждение при освобождении курьера

Доставка без кандидата ждёт, пока появится Courier Candidate. Use cases, после которых курьер становится кандидатом:

- Set Courier Availability → available при сохранённой позиции;
- Update Courier Location: первая сохранённая позиция available курьера;
- освобождение курьера: Confirm Delivery, Report Delivery Failure, Reassign Courier, Cancel Delivery назначенной доставки.

Register Courier не будит: у нового курьера нет позиции, кандидатом он станет на первом Update Courier Location.

После commit такой use case через порт `AssignmentWake` выбирает одну доставку и передаёт ей Wake с `reason = courier_available`:

- Market доставки совпадает с Market курьера;
- `Delivery` ждёт без pending offer;
- этому курьеру доставку ещё не предлагали;
- самая старая по `(created_at, id)`.

Выборка — SQL по `deliveries` и `delivery_offers` (partial index `deliveries_waiting` уже есть). Нет такой доставки — ничего не делается. Будится одна доставка: один курьер покрывает одно предложение, а остальные доставки дождутся следующего освобождения. Ошибка выборки или signal — `UNAVAILABLE`, как у остальных signals.

Если пробуждённая доставка выбрала другого, более близкого кандидата, курьер, вызвавший Wake, остаётся свободным, а следующие доставки не будятся. Это сознательное упрощение: Find Courier уже выбрал лучшего по расстоянию. Освободившийся курьер получит работу при следующем Wake любой доставки его Market.

## Почему не offer-workflow и не диспетчер на Market

- **Workflow на каждый offer** — короткий workflow на 60 s на каждое предложение, и ещё одно звено между offer и доставкой. Процесс назначения — свойство доставки, offer в нём лишь шаг.
- **Диспетчер на Market** сохраняет глобальный порядок, но это снова цикл над множеством доставок с собственным расписанием. Пробуждение самой старой подходящей доставки даёт тот же порядок без отдельного процесса.

## Deployable role `delivery-worker`

Отдельный бинарник `cmd/delivery-worker` в том же crate, как `orders-worker` у `orders`:

- composition root с графом use cases над тем же `Store`;
- Temporal worker task queue `delivery`, readiness — диагностическая (`docs/cloud-native-runtime.md`, раздел про Temporal workers);
- остановка: worker перестаёт брать задачи, начатые Activities доводятся в пределах `SHUTDOWN__DRAIN_TIMEOUT`.

Процесс `delivery` (API) получает Temporal client для start и signals.

Обязательный конфиг обоих процессов:

| Переменная | Назначение |
|---|---|
| `TEMPORAL__HOST_PORT` | адрес frontend Temporal |
| `TEMPORAL__NAMESPACE` | namespace |
| `TEMPORAL__CONNECT_TIMEOUT` | таймаут подключения, положительный |

Только `delivery-worker`: `ASSIGNMENT_WORKFLOW__CONTINUE_AS_NEW_AFTER` (положительный), retry policy и таймауты Activities (`ASSIGNMENT_WORKFLOW__ACTIVITY_START_TO_CLOSE`). Пустое или невалидное значение — падение на старте. Подключение к Temporal при старте — fail fast, как у `orders`.

## Temporal Rust SDK и OpenTelemetry

- `temporalio-sdk`, `temporalio-client`, `temporalio-sdk-core` 1.1.0 / 0.10.0. MSRV 1.92.
- SDK зависит от `opentelemetry` 0.32; PR на 0.33 в `temporalio/sdk-rust` (#1682, #1715) закрыты без merge. `delivery` переходит на `opentelemetry`, `opentelemetry_sdk`, `opentelemetry-otlp` 0.32 и оставляет у зависимостей в `Cargo.toml` комментарий: версия держится sdk-rust, вернуться на актуальную после обновления upstream.
- Trace context переносится через Temporal headers: start и signal несут текущий span, Activity продолжает его. Встроенная трассировка SDK (`experimental` + `opentelemetry`) не включается, пока она experimental.

## `orders`: ожидание назначения без срока

Модель fulfillment после Create Delivery ждёт `CourierAssignedToDelivery` без deadline. Удаляются:

- `Policy.AssignmentTimeout`, `MaxReplans`, `OperatorDecisionTimeout` и конфиг `FULFILLMENT__ASSIGNMENT_TIMEOUT`, `FULFILLMENT__MAX_REPLANS`, `FULFILLMENT__OPERATOR_DECISION_TIMEOUT`;
- шаг `ReplanDelivery`, Activity и клиент `ReplanDelivery`;
- состояние `escalated`, stage `waiting_operator`, `ExpiryEscalation`, signal `OperatorDecision`, причина `operator_decision`.

Ветка запланированного заказа (`add-scheduled-orders`) отсчитывала deadline Assignment от `CreateDelivery`: после изменения deadline нет, остаётся только момент `courier_request_at` для `CreateDelivery`.

Workflow `orders`, запущенные старым кодом, несовместимы с новым: окружение только для разработки, поэтому patch не вводится, kind сбрасывается.

## Риски

- **Temporal недоступен.** Create Delivery, ответы курьера и освобождение курьера возвращают `UNAVAILABLE`, хотя сама транзакция уже зафиксирована. Это fail closed: повтор вызова дотолкает signal. Курьерский UI и Activity `orders` вызов повторяют.
- **Нагрузка на Temporal.** Один долгоживущий workflow на доставку и по таймеру на offer — дешевле, чем проходы по таблицам каждые 2 s.
- **Experimental-части SDK** (deployment versioning, otel) не используются.
