# Spec Delta

## Purpose

Определяет durable orchestration общего жизненного цикла `Order`, надёжное взаимодействие `orders` с соседними bounded contexts и явные границы применения Temporal.

## ADDED Requirements

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

После атомарной записи `Order` и события `OrderPlaced` в Transactional Outbox контекст `orders` SHALL получить собственное событие через Kafka и Transactional Inbox, после чего выполнить `SignalWithStart` для `order:<order_id>` с конфликтной политикой `USE_EXISTING`.

#### Scenario: Событие получено до явного старта
- **WHEN** `OrderPlaced` первым достигает application handler
- **THEN** `SignalWithStart` создаёт workflow `order:<order_id>` и применяет событие к нему

#### Scenario: Повторная доставка OrderPlaced
- **WHEN** Kafka повторно доставляет тот же `event_id`
- **THEN** Inbox превращает повтор в no-op и второй workflow не создаётся

### Requirement: Допустим синхронный старт после commit

Как альтернативный путь система MAY запускать workflow синхронно только после успешного commit заказа и SHALL повторять неясный или временно неуспешный запуск с тем же Workflow ID. Этот путь MUST NOT заменять атомарную запись `Order` и `OrderPlaced` в Outbox.

#### Scenario: Ответ старта потерян
- **WHEN** Temporal применил запуск, но caller получил timeout и повторил запрос
- **THEN** повтор с `order:<order_id>` использует существующий workflow и не создаёт параллельный процесс

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
