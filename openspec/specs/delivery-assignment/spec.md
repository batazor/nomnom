# delivery-assignment Specification

## Purpose
Определяет, как `delivery` принимает запрос доставки от fulfillment, подбирает кандидата, предлагает ему `Delivery Offer`, превращает ровно один принятый offer в `Courier Assignment` и возвращает доставку в поиск при Reassignment.

## Requirements

### Requirement: Authoritative authorization сценариев назначения
**ID:** FR-305

Каждый сценарий назначения SHALL проверять право subject до доменной операции. Курьерские действия над offer MUST проверять право subject действовать от имени указанного `Courier`; принадлежность offer этому курьеру MUST дополнительно проверяться доменной моделью. Отказ authorization MUST NOT менять состояние.

#### Scenario: Subject не может действовать от имени курьера
**ID:** AC-312

- **WHEN** subject без права действовать от имени курьера пытается принять адресованный этому курьеру offer
- **THEN** сценарий отклоняется как permission denied, offer остаётся pending, `Delivery` остаётся waiting

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

### Requirement: Find Courier
**ID:** FR-307

Процесс назначения SHALL выбирать для waiting `Delivery` ближайшего кандидата среди available курьеров (расстояние считает локатор курьеров), удовлетворяющих `CourierEligibleForDelivery`, и ещё не получавших offer этой доставки. Отсутствие кандидата MUST возвращаться явным результатом с причинами отклонения кандидатов. Find Courier MUST NOT создавать offer или Assignment.

#### Scenario: Выбран ближайший подходящий курьер
**ID:** AC-317

- **WHEN** для waiting `Delivery` есть несколько available подходящих курьеров
- **THEN** выбран курьер с наименьшим расстоянием по данным локатора, а состояние `Delivery` и курьеров не меняется

#### Scenario: Подходящих курьеров нет
**ID:** AC-318

- **WHEN** все кандидаты busy, unavailable, вне зоны, слишком далеко, уже получали offer или исчерпали лимит назначений
- **THEN** возвращено отсутствие кандидата с причиной для каждого рассмотренного курьера

#### Scenario: Доставка уже назначена
**ID:** AC-319

- **WHEN** поиск запрошен для `Delivery`, которая не находится в состоянии waiting
- **THEN** сценарий отклоняется как нарушение precondition

### Requirement: Offer Delivery
**ID:** FR-308

Процесс назначения SHALL создавать для waiting `Delivery` и available `Courier` pending `Delivery Offer` со сроком действия из политики назначения. Offer MUST NOT создавать Assignment и менять `Courier` или `Delivery`. Повтор для той же пары при открытом offer MUST возвращать существующий offer.

#### Scenario: Создание offer
**ID:** AC-320

- **WHEN** процесс назначения предлагает waiting `Delivery` available курьеру
- **THEN** создан pending offer с моментом истечения, `Delivery` остаётся waiting, курьер остаётся available

#### Scenario: Повтор offer той же паре
**ID:** AC-321

- **WHEN** offer той же `Delivery` тому же курьеру запрашивается повторно до его истечения
- **THEN** возвращён существующий offer и второй offer не создаётся

#### Scenario: Курьер недоступен для offer
**ID:** AC-322

- **WHEN** offer запрашивается для busy или unavailable курьера
- **THEN** сценарий отклоняется как нарушение precondition и offer не создаётся

### Requirement: Accept Delivery Offer
**ID:** FR-309

Адресат pending `Delivery Offer` SHALL принимать его, пока offer удовлетворяет `DeliveryOfferAcceptable`; принятие MUST назначить курьера на waiting `Delivery`, сделать курьера busy, закрыть остальные pending offers этой доставки и записать факт Assignment в Outbox. Повтор принятия тем же курьером MUST быть no-op без второго факта.

#### Scenario: Курьер принимает актуальный offer
**ID:** AC-323

- **WHEN** адресат принимает непросроченный pending offer waiting `Delivery`
- **THEN** `Delivery` назначена этому курьеру, курьер busy, offer accepted, остальные pending offers доставки withdrawn и факт Assignment записан в Outbox

#### Scenario: Второй курьер последовательно принимает уже назначенную доставку
**ID:** AC-324

- **WHEN** после назначения `Delivery` другой курьер принимает свой pending offer той же доставки
- **THEN** сценарий отклоняется как нарушение precondition и Assignment, курьеры и Outbox не меняются

#### Scenario: Offer истёк
**ID:** AC-325

- **WHEN** курьер принимает offer позже момента его истечения
- **THEN** сценарий отклоняется с причиной expired и `Delivery` остаётся waiting

#### Scenario: Offer адресован другому курьеру
**ID:** AC-326

- **WHEN** курьер принимает offer, адресованный другому курьеру
- **THEN** сценарий отклоняется как нарушение precondition и offer остаётся pending

#### Scenario: Повтор принятия
**ID:** AC-327

- **WHEN** курьер повторно принимает уже принятый им offer
- **THEN** возвращено то же Assignment без изменений состояния и без второго факта в Outbox

### Requirement: Атомарность Assignment
**ID:** FR-310

Изменения offer, `Delivery`, `Courier` и запись факта Assignment в Outbox SHALL фиксироваться в одной локальной транзакции. При неуспешном commit MUST NOT сохраняться ни одно из этих изменений.

#### Scenario: Commit не удался
**ID:** AC-328

- **WHEN** транзакция принятия offer не может быть зафиксирована
- **THEN** сценарий завершается ошибкой, offer остаётся pending, `Delivery` waiting, курьер available и Outbox пуст

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

### Requirement: List Available Delivery Offers
**ID:** FR-313

Курьер SHALL получать список своих pending непросроченных `Delivery Offer` с `Order ID` и моментом истечения. Query MUST NOT менять состояние и MUST NOT включать accepted, rejected, withdrawn, просроченные offers и offers других курьеров.

#### Scenario: Список актуальных offers
**ID:** AC-333

- **WHEN** курьер с pending непросроченным offer запрашивает доступные offers
- **THEN** возвращён этот offer с `Order ID` и моментом истечения

#### Scenario: Закрытые и чужие offers не видны
**ID:** AC-334

- **WHEN** у курьера есть просроченный и отклонённый offers, а у другого курьера — pending offer
- **THEN** возвращённый курьеру список пуст

### Requirement: Занятость курьера меняется через слайс courier
**ID:** FR-485

Use cases слайса `delivery` SHALL проверять и менять занятость курьера только через прикладной порт слайса `courier` в текущей Unit of Work и MUST NOT вызывать методы агрегата `Courier` или `CourierRepository` напрямую. Атомарность FR-310 SHALL сохраняться.

#### Scenario: Принятие offer занимает курьера
**ID:** AC-549

- **WHEN** курьер принимает pending offer
- **THEN** курьер становится busy через порт `courier`, а `Delivery`, offer и `Courier` фиксируются одной транзакцией, как требует FR-310

#### Scenario: Сбой commit не оставляет курьера занятым
**ID:** AC-550

- **WHEN** commit транзакции принятия offer не удался
- **THEN** курьер остаётся available, offer pending, `Delivery` waiting

### Requirement: Слайсы delivery без циклических зависимостей
**ID:** FR-486

Слайс `courier` MUST NOT импортировать модули слайса `delivery`; зависимость допустима только в направлении `delivery → courier`.

#### Scenario: Проверка импортов
**ID:** AC-551

- **WHEN** проверка ищет `slices::delivery` в коде `slices/courier`
- **THEN** совпадений нет

### Requirement: Публикация снятия курьера и отмены Delivery
**ID:** FR-475

`delivery` SHALL публиковать Domain Events `CourierUnassignedFromDelivery` (`order_id`, `delivery_id`, `courier_id`) и `DeliveryCancelled` (`order_id`, `delivery_id`, `reason`) как Integration Events `nomnom.delivery.v1.*` стрима `delivery.v1.delivery-events` через Outbox той же транзакции. Отсутствующая причина отмены MUST передаваться пустым `reason`. Контракт MUST меняться только аддитивно.

#### Scenario: Снятие курьера и отмена попадают в Outbox
**ID:** AC-532

- **WHEN** Reassign Courier или Cancel Delivery сохраняет Delivery
- **THEN** Outbox содержит `CourierUnassignedFromDelivery` или `DeliveryCancelled` с полями контракта, а relay публикует их с envelope headers ADR 31

#### Scenario: События одной версии агрегата различимы
**ID:** AC-533

- **WHEN** одна транзакция пишет несколько событий одного `aggregate_version` (например, снятие и назначение курьера)
- **THEN** их `event_id` различаются, потому что UUIDv5 envelope включает `event_type`

### Requirement: Идентификаторы Delivery и offer уникальны между запусками
**ID:** FR-541

Production-процесс `delivery` SHALL выдавать идентификаторы `Delivery` и offer, уникальные между перезапусками и репликами; генератор, начинающий счёт заново при старте, MUST NOT использоваться вне тестов.

#### Scenario: Перезапуск процесса
**ID:** AC-631

- **WHEN** процесс создал `Delivery`, перезапустился и создаёт следующую
- **THEN** идентификатор новой `Delivery` не совпадает с ранее сохранённым

### Requirement: Поиск курьера опирается на сохранённые позиции
**ID:** FR-542

Production-локатор курьеров SHALL находить кандидатов среди доступных курьеров по их последним сохранённым позициям; пустой статический локатор MUST NOT использоваться вне тестов.

#### Scenario: Доступный курьер с позицией
**ID:** AC-632

- **WHEN** доступный курьер обновил позицию, и создаётся `Delivery`
- **THEN** Find Courier возвращает этого курьера как кандидата

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
