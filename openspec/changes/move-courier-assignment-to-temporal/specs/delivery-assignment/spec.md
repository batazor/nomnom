## ADDED Requirements

### Requirement: Assignment Workflow доставки
**ID:** FR-704

Назначением курьера каждой `Delivery` SHALL управлять Temporal workflow `delivery:<delivery_id>`. Каждый шаг SHALL выполняться Activity по сохранённому состоянию доставки; workflow MUST NOT хранить это состояние и MUST NOT использовать polling. Ожидающей доставке без pending offer шаг SHALL выполнять Find Courier и Offer Delivery в одной Unit of Work. Workflow SHALL завершаться после Pickup или закрытия доставки.

#### Scenario: Предложение сразу после создания
**ID:** AC-959

- **WHEN** создана `Delivery`, а Courier Candidate её Market есть
- **THEN** workflow без ожидания периодического прохода создаёт ему pending offer и записывает `DeliveryOffered` в Outbox

#### Scenario: Workflow завершается после Pickup
**ID:** AC-960

- **WHEN** назначенный курьер подтверждает Pickup
- **THEN** workflow `delivery:<delivery_id>` завершается успешно

#### Scenario: Поиск после Reassign
**ID:** AC-961

- **WHEN** диспетчер снимает курьера с назначенной, но не забранной `Delivery`, а другой Courier Candidate есть
- **THEN** workflow предлагает доставку другому курьеру

### Requirement: Истечение offer по таймеру
**ID:** FR-705

Для pending offer Assignment Workflow SHALL ставить Temporal timer на `expiresAt` и по нему закрывать offer как `expired` с `DeliveryOfferExpired` в Outbox, после чего SHALL предлагать доставку следующему курьеру. Offer, к моменту таймера уже принятый, отклонённый или отозванный, MUST оставаться без изменений. Pending offer с прошедшим `expiresAt`, найденный при чтении состояния, SHALL закрываться как `expired` в той же Unit of Work, что и следующее предложение.

#### Scenario: Offer истекает в срок
**ID:** AC-962

- **WHEN** курьер не ответил на offer до `expiresAt`, а другой Courier Candidate есть
- **THEN** сразу после `expiresAt` offer закрыт как `expired`, а доставка предложена другому курьеру

#### Scenario: Offer принят до таймера
**ID:** AC-963

- **WHEN** курьер принял offer до `expiresAt`
- **THEN** таймер не меняет offer, а `DeliveryOfferExpired` не записывается

### Requirement: Пробуждение доставки при появлении кандидата
**ID:** FR-706

Доставка без кандидата SHALL ждать signal без таймеров перепроверки. Use case, после commit которого курьер — Courier Candidate (available с сохранённой позицией: Set Courier Availability, сохранённая позиция, освобождение курьера), SHALL будить одну доставку: самую старую по `(created_at, id)` ожидающую `Delivery` его Market без pending offer, которой этого курьера ещё не предлагали. Нет такой доставки — signal MUST NOT передаваться.

#### Scenario: Курьер выходит на линию
**ID:** AC-964

- **WHEN** доставка ждёт без кандидата, и курьер с сохранённой позицией её Market становится available
- **THEN** workflow этой доставки просыпается и предлагает её этому курьеру

#### Scenario: Будится самая старая подходящая доставка
**ID:** AC-965

- **WHEN** освобождается курьер, а ждут две доставки его Market, и более старой его уже предлагали
- **THEN** signal получает более новая доставка, которой его не предлагали

### Requirement: Signals Assignment Workflow после commit
**ID:** FR-707

Accept и Reject Delivery Offer, Offer Delivery To Courier, Reassign Courier, Cancel Delivery, Confirm Pickup и Report Delivery Failure SHALL после commit будить workflow своей доставки; start и signals MUST NOT выполняться внутри Unit of Work. Ошибка start или signal SHALL возвращаться как `UNAVAILABLE`, а повтор вызова, не меняющий состояние, SHALL передавать signal заново. Signal завершённому workflow MUST NOT быть ошибкой.

#### Scenario: Temporal недоступен при отклонении offer
**ID:** AC-966

- **WHEN** курьер отклоняет offer, транзакция зафиксирована, а signal не прошёл
- **THEN** ответ `UNAVAILABLE`, offer остаётся rejected, а повтор отклонения успешно передаёт signal без изменения состояния

### Requirement: Deployable role delivery-worker
**ID:** FR-708

Assignment Workflow и его Activities SHALL исполняться отдельным процессом `delivery-worker` на task queue `delivery`. Процессы `delivery` и `delivery-worker` SHALL требовать `TEMPORAL__HOST_PORT`, `TEMPORAL__NAMESPACE` и `TEMPORAL__CONNECT_TIMEOUT`; пустое, неразбираемое или неположительное значение и недоступный Temporal MUST останавливать процесс на старте.

#### Scenario: Пустой адрес Temporal
**ID:** AC-967

- **WHEN** `delivery-worker` стартует с пустым `TEMPORAL__HOST_PORT`
- **THEN** процесс завершается с ненулевым кодом и сообщением о незаданной переменной

## MODIFIED Requirements

### Requirement: Идемпотентный Create Delivery
**ID:** FR-306

Fulfillment process SHALL создавать `Delivery` в состоянии waiting из `Order ID`. Ключ идемпотентности — `Order ID`: повтор MUST возвращать уже созданную `Delivery`. После commit Create Delivery SHALL запускать Assignment Workflow доставки (FR-707); поиск курьера выполняет workflow, а не сам Create Delivery. Адресов у `Delivery` нет: ни адреса доставки, ни pickup-адреса ресторана.

#### Scenario: Первый запрос доставки
**ID:** AC-313

- **WHEN** fulfillment process запрашивает доставку для `Order` без существующей `Delivery`
- **THEN** создана `Delivery` в состоянии waiting без курьера и возвращён её идентификатор

#### Scenario: Повтор того же запроса
**ID:** AC-314

- **WHEN** запрос доставки для того же `Order` повторяется
- **THEN** возвращён идентификатор ранее созданной `Delivery` и вторая `Delivery` не создаётся

#### Scenario: Повтор запускает тот же workflow
**ID:** AC-958

- **WHEN** запрос доставки повторяется после того, как первый запуск workflow не прошёл
- **THEN** запущен единственный workflow `delivery:<delivery_id>`, а при уже работающем workflow второй не создаётся

### Requirement: Reject Delivery Offer
**ID:** FR-311

Адресат pending непросроченного `Delivery Offer` SHALL отклонять его; отклонённый offer MUST NOT создавать Assignment. Повтор отклонения тем же курьером MUST быть no-op. После commit отклонение SHALL будить Assignment Workflow доставки, который предлагает её следующему курьеру.

#### Scenario: Курьер отклоняет offer
**ID:** AC-329

- **WHEN** адресат отклоняет pending offer
- **THEN** offer закрыт как rejected, `Delivery` остаётся waiting и курьер остаётся available

#### Scenario: Повтор отклонения
**ID:** AC-330

- **WHEN** курьер повторно отклоняет уже отклонённый им offer
- **THEN** сценарий завершается успешно без изменения состояния

### Requirement: Reassign Courier
**ID:** FR-312

Процесс назначения или авторизованный диспетчер SHALL прекращать Assignment назначенной, но ещё не забранной `Delivery`: доставка MUST вернуться в waiting без курьера, прежний курьер MUST стать available, а факт снятия назначения MUST быть записан в Outbox в той же транзакции. После commit Reassign SHALL будить Assignment Workflow доставки, который возвращает её в поиск.

#### Scenario: Переназначение до Pickup
**ID:** AC-331

- **WHEN** диспетчер запрашивает Reassignment назначенной `Delivery`
- **THEN** `Delivery` waiting без курьера, прежний курьер available и факт снятия назначения записан в Outbox

#### Scenario: Переназначение после Pickup
**ID:** AC-332

- **WHEN** Reassignment запрашивается для забранной `Delivery`
- **THEN** сценарий отклоняется как нарушение precondition и Assignment сохраняется

## REMOVED Requirements

### Requirement: Dispatch ожидающих доставок
**ID:** FR-582

**Reason**: периодические проходы Dispatch Deliveries и истечения offers заменены Assignment Workflow на каждую доставку (FR-704, FR-705, FR-706).

**Migration**: `DispatchWorker`, `OfferExpiryWorker`, конфиг `OFFERS__DISPATCH_*` и `OFFERS__EXPIRY_*`, query-порты `WaitingDeliveries` и `ExpiredDeliveryOffers` удаляются. Сценарии AC-734…AC-736 покрываются AC-959, AC-962 и AC-964.
