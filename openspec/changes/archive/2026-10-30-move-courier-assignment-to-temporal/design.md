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

Signal один: `wake` с полем `reason` (`offer_made`, `offer_answered`, `courier_available`, `courier_unassigned`, `delivery_closed`) — только для диагностики, на ход workflow не влияет. Дедупликации не нужно: лишний Wake приводит к ещё одному чтению состояния.

Event History растёт с каждой итерацией. Когда сервер подсказывает continue-as-new (`continue_as_new_suggested`), workflow продолжается новым run: состояния у него нет, поэтому переносить нечего.

## Запуск и signals после commit

Temporal не входит в Unit of Work (`docs/unit-of-work.md`), поэтому start и signal выполняются после commit, через порт `AssignmentProcess` в `applications/` с адаптером в `infrastructure/temporal`.

- **Create Delivery** после commit запускает workflow с политикой `WorkflowIdConflictPolicy::UseExisting`, и на первом вызове, и на повторе, который возвращает уже созданную `Delivery`. Ошибка запуска — `UNAVAILABLE`. Create Delivery вызывается Activity `orders`, и её retry повторяет вызов: потеря запуска закрывается без Outbox-relay.
- **Accept, Reject Delivery Offer, Offer Delivery To Courier, Reassign Courier, Cancel Delivery, Confirm Pickup, Report Delivery Failure** после commit передают Wake своей доставки.
- **Ошибка signal — `UNAVAILABLE`.** Повтор вызова, который уже не меняет состояние (`AlreadyApplied`, повтор того же ответа), всё равно передаёт Wake. Для Accept и Reject потеря signal не блокирует процесс: таймер offer всё равно сработает. Для остальных повтор вызова обязателен, иначе workflow будет ждать.
- Wake закрытому workflow (доставка уже завершила назначение) — не ошибка.

## Пробуждение при освобождении курьера

Доставка без кандидата ждёт, пока появится Courier Candidate. Use cases, после которых курьер становится кандидатом:

- Set Courier Availability → available при сохранённой позиции;
- Update Courier Location: сохранённая позиция available курьера (запись прореживается `TRACKING__PERSIST_INTERVAL`; лишний Wake только перечитывает состояние);
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

Пустое или невалидное значение — падение на старте. Подключение к Temporal при старте — fail fast, как у `orders`. Таймауты и retry policy Activities (30 s start-to-close, backoff 1 s × 2 до 60 s) — константы кода workflow, как в `orders`; отдельного конфига нет. `delivery-worker` читает из `CONFIG_FILE` только `management`, `shutdown`, `database`, `temporal`.

## Temporal Rust SDK и OpenTelemetry

- `temporalio-sdk`, `temporalio-client`, `temporalio-common` 1.1.0 (`temporalio-sdk-core` 0.10.0). MSRV 1.92.
- SDK подключён без default features: `opentelemetry` 0.32 SDK тянет только с features `otel`/`opentelemetry`, поэтому `delivery` остаётся на своей версии 0.33 и понижать её не нужно. `vendored-protox` компилирует protos SDK без `protoc`.
- Workflows SDK исполняет на не-`Send` futures, поэтому worker живёт в собственном потоке с current-thread runtime; lifecycle процесса видит сигнал остановки и завершение потока.
- Spans и `nomnom.usecase.duration` шагов (`advance_assignment`, `expire_delivery_offer`) пишет `delivery-worker`. Trace context через Temporal headers не переносится: шаги идут часами после старта, общий trace с Create Delivery не нужен; встроенная трассировка SDK experimental.

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
