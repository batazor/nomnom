# order-fulfillment-orchestration Specification

## Purpose
Определяет durable orchestration общего жизненного цикла `Order`, надёжное взаимодействие `orders` с соседними bounded contexts и явные границы применения Temporal.

## Requirements

### Requirement: Один workflow на один логический Order

Система SHALL выполнять Order Fulfillment Workflow с Workflow ID `order:<order_id>`. Повторный запуск для того же `Order` MUST использовать существующий workflow, а не создавать параллельный процесс.

#### Scenario: Повторная команда запуска
- **WHEN** запуск одного и того же `Order` запрашивается повторно после таймаута caller или повторной доставки события
- **THEN** система адресует workflow `order:<order_id>` и не создаёт второй Order Fulfillment Workflow

#### Scenario: Изоляция окружений и обработчиков
- **WHEN** Order Fulfillment Workflow запускается в конкретном окружении
- **THEN** он использует Temporal namespace этого окружения и task queue `orders`

### Requirement: Durable orchestration применяется только к долгоживущему процессу

Temporal SHALL использоваться для многошагового процесса, который содержит внешние зависимости, ожидания, retries, таймеры или компенсации и должен переживать перезапуск worker. Локальные операции в пределах одного запроса и транзакции MUST завершаться без workflow.

#### Scenario: Процесс переживает остановку worker
- **WHEN** worker останавливается во время ожидания результата ресторана, курьера или доставки
- **THEN** состояние ожидания сохраняется Temporal и обработка продолжается после доступности worker

#### Scenario: Локальное изменение Order
- **WHEN** use case может выполнить проверку, изменение `Order` и commit в одной короткой локальной транзакции
- **THEN** use case выполняется без запуска Temporal workflow

### Requirement: Основной старт через OrderPlaced

После атомарной записи `Order` и события `OrderPlaced` в Transactional Outbox контекст `orders` SHALL получить собственное событие через Kafka и Transactional Inbox, после чего выполнить `SignalWithStart` для `order:<order_id>` с конфликтной политикой `USE_EXISTING`. Это единственный путь старта Order Fulfillment Workflow.

#### Scenario: Событие получено до явного старта
- **WHEN** `OrderPlaced` первым достигает application handler
- **THEN** `SignalWithStart` создаёт workflow `order:<order_id>` и применяет событие к нему

#### Scenario: Повторная доставка OrderPlaced
- **WHEN** Kafka повторно доставляет тот же `event_id`
- **THEN** Inbox превращает повтор в no-op и второй workflow не создаётся

#### Scenario: Сбой между commit Inbox и SignalWithStart
**ID:** AC-819

- **GIVEN** Inbox зафиксировал `event_id` `OrderPlaced`, а процесс остановился до `SignalWithStart`
- **WHEN** Kafka повторно доставляет то же сообщение
- **THEN** Inbox распознаёт дубликат, после commit выполняется `SignalWithStart`, и workflow `order:<order_id>` запущен

### Requirement: Старт по OrderPlaced пропускает завершённые заказы и не теряет сообщения
**ID:** FR-643

Для `Order` в `completed` или `cancelled` consumer `OrderPlaced` MUST NOT отправлять сигнал. Ошибка Temporal SHALL оставлять сообщение неподтверждённым для повторной доставки и MUST NOT отправлять его в DLQ. Собственные `OrderCompleted` и `OrderCancelled` того же топика consumer `orders` SHALL подтверждать без действия.

#### Scenario: OrderPlaced завершённого заказа
**ID:** AC-820

- **WHEN** consumer получает `OrderPlaced` для `Order` в `completed` или `cancelled`, например после redrive DLQ
- **THEN** сообщение подтверждается, `SignalWithStart` не выполняется и workflow не создаётся заново

#### Scenario: Temporal недоступен
**ID:** AC-821

- **WHEN** `SignalWithStart` завершается ошибкой Temporal
- **THEN** сообщение не подтверждается и доставляется повторно, в `orders.v1.order-events.dlq` ничего не записано

#### Scenario: Собственные завершающие события
**ID:** AC-822

- **WHEN** consumer `orders` получает `OrderCompleted` или `OrderCancelled`
- **THEN** сообщение подтверждается без бизнес-действия и не попадает в DLQ

### Requirement: Workflow координирует, но не владеет соседними доменами

Order Fulfillment Workflow SHALL координировать Authorization, Order Acceptance, Courier Assignment, Preparation, Pickup, Capture и Delivery Completion. Правила и инварианты этих операций MUST оставаться в `payments`, `restaurants` и `delivery`.

#### Scenario: Запрос финансовой операции
- **WHEN** workflow требует Authorization, Capture, отмену Authorization или Refund
- **THEN** Activity вызывает бизнес-API `payments` через internal gateway от собственной `service` identity

#### Scenario: Запрос назначения курьера
- **WHEN** workflow требует доставку
- **THEN** он создаёт Delivery и ожидает результат, не выполняя алгоритм dispatch внутри workflow

### Requirement: Финансовая операция имеет стабильную идемпотентность

Каждая финансовая Activity SHALL передавать в `payments` idempotency key, детерминированно составленный из `order_id` и логического шага. Ключ MUST NOT зависеть от номера attempt или Temporal run ID.

#### Scenario: Неизвестный результат финансового вызова
- **WHEN** Activity не получила ответ после того, как `payments` мог применить операцию
- **THEN** повтор использует тот же idempotency key и не создаёт вторую логическую финансовую операцию

#### Scenario: Business rejection платежа
- **WHEN** `payments` возвращает card declined или иной бизнес-отказ
- **THEN** workflow обрабатывает результат как бизнес-ветвление и не использует technical retry для изменения решения

### Requirement: Подтверждение ресторана поступает событием

Workflow SHALL инициировать запрос Order Acceptance и durable-ждать accepted, rejected или продуктового timeout. Результат MUST поступать как Kafka event через Inbox и Signal; polling ресторанного API запрещён.

#### Scenario: Ресторан принимает Restaurant Order
**ID:** AC-001

- **WHEN** `orders` получает подтверждённое событие Order Acceptance
- **THEN** application handler отправляет сигнал workflow, и orchestration переходит к допустимому следующему шагу

#### Scenario: Истёк срок ожидания ресторана
- **WHEN** Temporal timer достигает продуктового timeout без accepted или rejected
- **THEN** workflow начинает предусмотренную policy компенсацию

### Requirement: Назначение курьера допускает replan и решение оператора

После создания Delivery workflow SHALL ждать Courier Assignment. При timeout он SHALL выполнить retry, replan или Order Cancellation согласно бизнес-политике; при предусмотренной эскалации SHALL ждать подтверждённое решение оператора вместе с бизнес-таймером.

#### Scenario: Курьер назначен
**ID:** AC-002

- **WHEN** `orders` получает событие Courier Assignment
- **THEN** Inbox и application handler доставляют сигнал текущему workflow, который продолжает процесс без самостоятельного выбора Courier

#### Scenario: Автоматическое назначение исчерпано
- **WHEN** business policy требует вмешательства после неуспешных попыток назначения
- **THEN** workflow durable-ждёт `OperatorDecision` до решения или заданного бизнес-таймера

### Requirement: Входящее событие проходит Inbox и несёт event_id

События `restaurants`, `delivery` и `payments` SHALL поступать по пути `Kafka → orders consumer → Inbox → application handler → Signal/Update`. Соседние контексты MUST NOT вызывать Temporal напрямую. Каждый Signal SHALL содержать исходный `event_id`.

#### Scenario: Повтор Signal RPC после timeout
**ID:** AC-003

- **WHEN** handler не знает, применил ли Temporal предыдущий Signal, и повторяет RPC с тем же `event_id`
- **THEN** workflow-handler распознаёт уже обработанный идентификатор и не повторяет переход или побочный эффект

#### Scenario: Положительное событие не соответствует текущему Workflow Stage
- **WHEN** незавершённый workflow получает новое положительное событие, которое текущий Workflow Stage не ожидает
- **THEN** handler игнорирует событие, записывает диагностический log и не завершает workflow ошибкой

#### Scenario: Отрицательное событие вне ожидаемого Workflow Stage
- **WHEN** незавершённый workflow получает `RestaurantOrderRejected`, `PreparationFailed`, `DeliveryFailed` или `PaymentFailed` в stage, которая не ждёт этот факт
- **THEN** workflow не игнорирует событие молча, а запускает предусмотренную Saga либо переходит в эскалацию с диагностикой

### Requirement: Signal называется по Integration Event

Имя Signal SHALL совпадать с именем Integration Event, вызвавшего его. Application-порт `orders` MUST оставаться независимым от имён Temporal: перевод `fulfillment.Kind` в имя Signal SHALL выполнять Temporal-адаптер. Исходный набор перечислен в design.md change.

#### Scenario: Signal от события ресторана
- **WHEN** consumer `orders` обработал `RestaurantOrderAccepted` через Inbox
- **THEN** Temporal-адаптер отправляет Signal с именем `RestaurantOrderAccepted` и исходным `event_id`

#### Scenario: Отказ ресторана после Acceptance
- **WHEN** `restaurants` публикует `RestaurantOrderRejected` после того, как workflow уже получил `RestaurantOrderAccepted`, и Pickup ещё не произошёл
- **THEN** workflow компенсирует выполненные шаги по Saga, а не игнорирует событие как неожиданное

#### Scenario: Задержка приготовления
- **WHEN** workflow ждёт окончания Preparation и получает `PreparationDelayed`
- **THEN** бизнес-таймер ожидания Preparation продлевается по policy, и компенсация по исходному сроку не запускается

### Requirement: Поздние события имеют явную policy

Каждый use case SHALL определить обработку Signal для завершённого workflow: диагностируемое игнорирование либо запуск отдельного compensation flow. Неопределённая автоматическая перезапись завершённого `Order` запрещена.

#### Scenario: Позднее событие для завершённого workflow
- **WHEN** событие достигает `orders` после завершения Order Fulfillment Workflow
- **THEN** система применяет документированную для этого события policy и сохраняет traceable результат обработки

### Requirement: Signal, Update и Query имеют разные роли

Асинхронное уведомление SHALL передаваться Signal. Операция, которой нужен подтверждённый результат или синхронная validation Workflow Stage, SHALL использовать Update. Клиентское чтение Order MUST использовать CQRS read model; Query workflow допустим только для технической необходимости.

#### Scenario: Клиент читает карточку Order
- **WHEN** клиент запрашивает `GetOrder` или список заказов
- **THEN** `orders` отвечает из CQRS read model без Temporal Query

#### Scenario: Команда требует подтверждения принятия workflow
- **WHEN** caller должен получить результат validation относительно текущего Workflow Stage
- **THEN** контракт использует Update, а не fire-and-forget Signal

### Requirement: CancelOrder является бизнес-командой

Клиентская отмена SHALL поступать в существующий workflow как Signal или Update, а не через Temporal `CancelWorkflow`. Домен `orders` MUST определить допустимость Order Cancellation, а workflow SHALL определить компенсации уже выполненных шагов.

#### Scenario: Отмена до необратимого состояния
- **WHEN** доменная модель разрешает Order Cancellation в текущем состоянии
- **THEN** workflow фиксирует отмену через use case `orders` и выполняет необходимые компенсации

#### Scenario: Отмена запрещена доменом
- **WHEN** `Order` достиг состояния, в котором отмена запрещена
- **THEN** Update возвращает бизнес-отказ без технической отмены workflow

### Requirement: Компенсации образуют orchestration-based Saga

Каждая компенсация SHALL выполняться отдельной идемпотентной, retryable и наблюдаемой Activity без зависимости от process memory. Workflow SHALL компенсировать только успешно выполненные логические шаги в порядке, определённом бизнес-политикой.

#### Scenario: Отказ после Authorization и принятия рестораном
- **WHEN** последующий шаг делает продолжение Order невозможным
- **THEN** workflow вызывает необходимые CancelRestaurantOrder и CancelPaymentAuthorization либо Refund согласно уже зафиксированным результатам

#### Scenario: Повтор компенсации после crash
- **WHEN** worker падает после применения компенсации, но до подтверждения Activity result
- **THEN** повтор с тем же idempotency key не создаёт второй логический эффект

### Requirement: Technical retry отделён от business outcome

Activity SHALL иметь явные Start-To-Close timeout и Retry Policy для временных технических отказов. Длительная Activity SHALL отправлять heartbeat. Business rejection MUST возвращаться как результат, не подлежащий technical retry.

#### Scenario: Временная недоступность gRPC
- **WHEN** Activity получает transport timeout или `UNAVAILABLE`
- **THEN** Temporal повторяет Activity в пределах её Retry Policy

#### Scenario: Worker останавливается во время длительной Activity
- **WHEN** heartbeat прекращается и Activity считается потерянной
- **THEN** Temporal повторяет Activity согласно policy, сохраняя тот же ключ логической операции

### Requirement: Длительность orchestration ограничивается бизнес-таймерами

Order Fulfillment Workflow MUST NOT иметь workflow-level timeout, который технически обрывает общий процесс. Ожидания SHALL ограничиваться отдельными Temporal timers, выражающими продуктовые сроки и ведущими к явному бизнес-результату.

#### Scenario: Длительный, но допустимый процесс
- **WHEN** Order остаётся в разрешённом ожидании дольше обычного времени выполнения worker task
- **THEN** workflow сохраняется до события или соответствующего бизнес-таймера

### Requirement: Kafka и Temporal выполняют разные функции

Kafka SHALL оставаться транспортом интеграционных событий между bounded contexts, а Temporal SHALL хранить состояние orchestration одного Order. Temporal MUST NOT заменять event bus, а Kafka event MUST NOT считаться заменой Workflow Stage.

#### Scenario: Соседний контекст сообщает результат
- **WHEN** `delivery` завершает Delivery
- **THEN** он публикует интеграционное событие через свой Outbox и Kafka, а `orders` преобразует обработанное Inbox событие в Signal workflow

### Requirement: Изменение Order публикуется через Outbox

Application operation или Activity, изменяющая состояние `orders`, SHALL атомарно сохранить business state и Outbox record. Activity MUST NOT публиковать в Kafka напрямую. Повтор операции после commit MUST сохранять идентичность логического события.

#### Scenario: Crash после commit orders
- **WHEN** состояние `Order` и Outbox record зафиксированы, но процесс падает до публикации
- **THEN** Outbox publisher позднее доставляет событие с тем же стабильным `event_id`

#### Scenario: Activity повторена после успешного commit
- **WHEN** Temporal повторяет Activity из-за неизвестного результата
- **THEN** идемпотентная операция не создаёт второе логическое изменение или новый `event_id` для того же изменения

### Requirement: Order State и Workflow Stage разделены

Состояния `placed`, `confirmed`, `cancelled`, `completed` SHALL принадлежать доменной модели `Order`. Состояния ожидания, retry и compensation SHALL принадлежать Temporal workflow. Temporal storage MUST NOT быть source of truth или репозиторием `Order`.

#### Scenario: Workflow ожидает готовность
- **WHEN** orchestration durable-ждёт окончание Preparation
- **THEN** стадия ожидания хранится в workflow, а доступное клиенту состояние заказа читается из `orders`

#### Scenario: Наблюдаемость orchestration
- **WHEN** оператор ищет workflows по стадии процесса
- **THEN** workflow публикует низкокардинальную orchestration stage как Search Attribute без превращения её в доменное состояние `Order`

### Requirement: Workflow остаётся детерминированным

Workflow code MUST NOT выполнять gRPC, HTTP, DB, Kafka, SpiceDB или иные side effects. Такие операции SHALL выполняться Activities с явными input, result, timeout, retry и idempotency semantics.

#### Scenario: Workflow replay после перезапуска
- **WHEN** Temporal воспроизводит Event History
- **THEN** workflow принимает те же решения без повторного прямого обращения к внешней системе

### Requirement: Temporal не заменяет прикладные и инфраструктурные механизмы

Temporal MUST NOT использоваться для CQRS queries, локального `request → transaction → response`, доставки Kafka events, authorization, хранения агрегатов или как универсальная job queue. SpiceDB checks SHALL оставаться в защищённых use cases соответствующих bounded contexts.

#### Scenario: Activity выполняет защищённый use case
- **WHEN** Activity вызывает бизнес-API другого bounded context
- **THEN** целевой context авторизует конкретную `service` identity, а workflow не принимает authorization-решение вместо него

#### Scenario: Нужно доставить интеграционное событие
- **WHEN** bounded context сообщает факт другим boundaries
- **THEN** используется Transactional Outbox, Kafka и Transactional Inbox, а не Temporal task queue

### Requirement: Маршрут workflow выполняется шагами ADR 5
**ID:** FR-415

Order Fulfillment Workflow SHALL выполнять AuthorizePayment, затем RequestRestaurantOrder, после `RestaurantOrderAccepted` — CreateDelivery, после `DeliveryPickedUp` — CapturePayment, после `DeliveryCompleted` — CompleteOrder. Activities MUST вызываться только из главного цикла workflow, а не из обработчиков Signals.

#### Scenario: Успешный заказ
**ID:** AC-433

- **WHEN** workflow получает Authorization, `RestaurantOrderAccepted`, `CourierAssignedToDelivery`, `RestaurantOrderReady`, `DeliveryPickedUp` и `DeliveryCompleted`
- **THEN** Activities выполняются по одному разу в порядке маршрута, и workflow завершается Completion

#### Scenario: Отказ Authorization
**ID:** AC-434

- **WHEN** `payments` отклоняет Authorization
- **THEN** workflow вызывает FailOrCancelOrder с видом failure и кодом `payment_declined` без компенсаций

### Requirement: Deadlines берутся из Policy во входе workflow
**ID:** FR-416

Каждое ожидание SHALL ограничиваться deadline из `Policy`, переданной во входе workflow. `PreparationDelayed` SHALL продлевать deadline Preparation до сообщённого времени, но не дальше предела Policy.

#### Scenario: Истёк срок Acceptance
**ID:** AC-435

- **WHEN** `RestaurantOrderAccepted` не приходит до deadline Acceptance
- **THEN** workflow отменяет Restaurant Order и Authorization и завершает Order кодом `restaurant_acceptance_timeout`

#### Scenario: Задержка приготовления продлевает срок
**ID:** AC-436

- **WHEN** до deadline Preparation приходит `PreparationDelayed` с более поздним ожидаемым временем
- **THEN** компенсация по исходному deadline не запускается

### Requirement: Assignment допускает replan и решение оператора
**ID:** FR-417

При истечении deadline Assignment workflow SHALL вызвать ReplanDelivery, пока число replan не превысит Policy, затем SHALL перейти в stage `waiting_operator` и ждать `OperatorDecision` до deadline оператора.

#### Scenario: Оператор отменяет заказ
**ID:** AC-437

- **WHEN** после исчерпания replan приходит `OperatorDecision` с решением cancel
- **THEN** workflow компенсирует выполненные шаги и завершает Order как cancellation с кодом `operator_decision`

### Requirement: Saga компенсирует по журналу шагов
**ID:** FR-418

Workflow SHALL компенсировать только выполненные и не закрытые владельцем шаги в обратном порядке: CancelDelivery, CancelRestaurantOrder, затем RefundPayment после Capture или CancelPaymentAuthorization. После компенсаций workflow SHALL вызвать FailOrCancelOrder с видом итога и кодом причины.

#### Scenario: Отказ ресторана после Acceptance
**ID:** AC-438

- **WHEN** после создания Delivery приходит `RestaurantOrderRejected`
- **THEN** workflow вызывает CancelDelivery и CancelPaymentAuthorization, не вызывает CancelRestaurantOrder и завершает Order кодом `restaurant_rejected`

#### Scenario: Неудача доставки после Capture
**ID:** AC-439

- **WHEN** после Capture приходит `DeliveryFailed`
- **THEN** workflow вызывает RefundPayment вместо CancelPaymentAuthorization

### Requirement: Signals дедуплицируются и проверяются по stage
**ID:** FR-419

Workflow SHALL игнорировать повтор `event_id` и положительный Signal, недопустимый в текущем состоянии, записывая диагностический log и не меняя маршрут.

#### Scenario: Повтор Acceptance
**ID:** AC-440

- **WHEN** `RestaurantOrderAccepted` с тем же `event_id` приходит дважды
- **THEN** CreateDelivery выполняется один раз

#### Scenario: Готовность до Acceptance
**ID:** AC-441

- **WHEN** `RestaurantOrderReady` приходит до `RestaurantOrderAccepted`
- **THEN** workflow игнорирует его, и последующий маршрут ждёт повторной готовности

### Requirement: Cancellation принимается Update до Pickup
**ID:** FR-420

Update `CancellationRequested` SHALL быть принят до Pickup, если workflow не завершает и не компенсирует заказ; иначе validator SHALL отклонить его ошибкой `CancellationRefused`. Повтор принятого `RequestID` SHALL приниматься без повторных эффектов.

#### Scenario: Отмена до Acceptance
**ID:** AC-442

- **WHEN** клиент отменяет заказ, ожидающий Acceptance
- **THEN** workflow отменяет Restaurant Order и Authorization и завершает Order как cancellation с кодом `customer_requested`

#### Scenario: Отмена после Pickup
**ID:** AC-443

- **WHEN** Update приходит после `DeliveryPickedUp`
- **THEN** validator отклоняет его, и маршрут продолжается

### Requirement: Client-адаптер реализует порт fulfillment
**ID:** FR-421

Client-адаптер SHALL стартовать workflow с Workflow ID `order:<order_id>`, task queue `orders`, политиками `USE_EXISTING` и `REJECT_DUPLICATE`, отправлять Signal с именем Integration Event по `fulfillment.Kind` и передавать Cancellation Update с Update ID, равным `RequestID`. Отсутствующий или завершённый workflow MUST возвращаться как `ErrWorkflowNotFound`, отказ validator — как `ErrCancellationRefused`.

#### Scenario: Signal завершённому workflow
**ID:** AC-444

- **WHEN** Temporal отвечает `NotFound` на Signal
- **THEN** `Notify` возвращает `ErrWorkflowNotFound`

#### Scenario: Повторный старт завершённого workflow
**ID:** AC-445

- **WHEN** `Start` вызывается для `Order`, чей workflow уже завершён
- **THEN** адаптер не создаёт новый run и возвращает успех

### Requirement: Решения процесса принимает доменная модель
**ID:** FR-483

Правила Order Fulfillment — следующий шаг маршрута, реакция на факты соседей, сроки, replan и эскалация, допустимость Cancellation Request и план компенсации — SHALL реализовываться доменной моделью слайса `fulfillment`, которая MUST NOT зависеть от Temporal SDK, часов и случайности. Temporal workflow SHALL только переводить Signals и Updates в вызовы модели и исполнять её решения.

#### Scenario: Replan проверяется без Temporal
**ID:** AC-546

- **WHEN** unit-тест модели применяет к процессу с `MaxReplans = 2` три истечения срока назначения курьера
- **THEN** модель дважды возвращает шаг `ReplanDelivery`, а на третий раз переводит процесс в ожидание решения оператора

#### Scenario: Компенсация по журналу шагов
**ID:** AC-547

- **WHEN** процесс с подтверждёнными Authorization и запросом в ресторан получает отказ ресторана
- **THEN** модель возвращает план компенсации из отмены Authorization и закрытия заказа как отказа, без отмены доставки, которая не создавалась

### Requirement: Внешние имена процесса не меняются при выделении слайса
**ID:** FR-484

Выделение слайса `fulfillment` MUST сохранять имена workflow, Signals, Update, Query, Activities, Task Queue, Search Attribute `OrderFulfillmentStage` и формат Input/Result.

#### Scenario: Сквозной сценарий после переноса
**ID:** AC-548

- **WHEN** тест адаптера на Temporal test environment проходит заказ от `OrderPlaced` до завершения
- **THEN** вызываются те же Activities с теми же именами и idempotency key шагов, что до переноса, а результат — `completed`

### Requirement: Ресторан запрашивается после фиксации оплаты
**ID:** FR-636

Workflow SHALL запрашивать Restaurant Order (`RequestRestaurantOrder`) только после одобренной Authorization и записи факта оплаты в `Order`. Restaurant Order MUST создаваться только этим шагом; при отказе оплаты Restaurant Order MUST NOT появляться.

#### Scenario: Оплата одобрена
**ID:** AC-823

- **WHEN** `payments` одобряет Authorization
- **THEN** workflow записывает факт оплаты в `Order`, затем запрашивает Restaurant Order, и кухня видит заказ только после этого

#### Scenario: Оплата отклонена
**ID:** AC-824

- **WHEN** `payments` отклоняет Authorization
- **THEN** workflow завершает `Order` с reason `payment_declined`, Restaurant Order не создан, компенсация ресторана не требуется

### Requirement: Оплата фиксируется в Order шагом workflow
**ID:** FR-637

После одобренной Authorization workflow SHALL выполнить шаг `RecordPaymentAuthorization`: Activity вызывает use case Record Payment Authorization слайса `order` с моментом решения. Шаг MUST быть идемпотентным при повторе Activity и не имеет компенсации.

#### Scenario: Повтор Activity
**ID:** AC-825

- **WHEN** Activity `RecordPaymentAuthorization` повторяется после того, как факт уже записан
- **THEN** шаг завершается успешно, второй факт не записан, маршрут переходит к `RequestRestaurantOrder`
