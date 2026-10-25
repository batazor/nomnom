# Spec Delta

## Purpose

Определяет, как `delivery` принимает запрос доставки от fulfillment, подбирает кандидата, предлагает ему `Delivery Offer`, превращает ровно один принятый offer в `Courier Assignment` и возвращает доставку в поиск при Reassignment.

## ADDED Requirements

### Requirement: Authoritative authorization сценариев назначения
**ID:** FR-305

Каждый сценарий назначения SHALL проверять право subject до доменной операции. Курьерские действия над offer MUST проверять право subject действовать от имени указанного `Courier`; принадлежность offer этому курьеру MUST дополнительно проверяться доменной моделью. Отказ authorization MUST NOT менять состояние.

#### Scenario: Subject не может действовать от имени курьера
**ID:** AC-312

- **WHEN** subject без права действовать от имени курьера пытается принять адресованный этому курьеру offer
- **THEN** сценарий отклоняется как permission denied, offer остаётся pending, `Delivery` остаётся waiting

### Requirement: Идемпотентный Create Delivery
**ID:** FR-306

Fulfillment process SHALL создавать `Delivery` в состоянии waiting из `Order ID`, pickup и dropoff адресов. Ключ идемпотентности — `Order ID`: повтор с теми же данными MUST возвращать уже созданную `Delivery`, повтор с другими адресами MUST отклоняться как already exists. Создание MUST NOT запускать поиск курьера.

#### Scenario: Первый запрос доставки
**ID:** AC-313

- **WHEN** fulfillment process запрашивает доставку для `Order` без существующей `Delivery`
- **THEN** создана `Delivery` в состоянии waiting без курьера и возвращён её идентификатор

#### Scenario: Повтор того же запроса
**ID:** AC-314

- **WHEN** запрос доставки для того же `Order` с теми же адресами повторяется
- **THEN** возвращён идентификатор ранее созданной `Delivery` и вторая `Delivery` не создаётся

#### Scenario: Конфликтующий повтор
**ID:** AC-315

- **WHEN** для `Order` с существующей `Delivery` приходит запрос с другими адресами
- **THEN** сценарий отклоняется как already exists и существующая `Delivery` не меняется

#### Scenario: Пустой адрес
**ID:** AC-316

- **WHEN** pickup или dropoff адрес пуст после нормализации пробелов
- **THEN** сценарий отклоняется как некорректный запрос и `Delivery` не создаётся

### Requirement: Find Courier
**ID:** FR-307

Процесс назначения SHALL выбирать для waiting `Delivery` ближайшего к pickup кандидата среди available курьеров, удовлетворяющих `CourierEligibleForDelivery`, и ещё не получавших offer этой доставки. Отсутствие кандидата MUST возвращаться явным результатом с причинами отклонения кандидатов. Find Courier MUST NOT создавать offer или Assignment.

#### Scenario: Выбран ближайший подходящий курьер
**ID:** AC-317

- **WHEN** для waiting `Delivery` есть несколько available подходящих курьеров
- **THEN** выбран курьер с наименьшим расстоянием до pickup, а состояние `Delivery` и курьеров не меняется

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

Адресат pending непросроченного `Delivery Offer` SHALL отклонять его; отклонённый offer MUST NOT создавать Assignment. Повтор отклонения тем же курьером MUST быть no-op. Отклонение MUST NOT запускать поиск курьера автоматически.

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

Процесс назначения или авторизованный диспетчер SHALL прекращать Assignment назначенной, но ещё не забранной `Delivery`: доставка MUST вернуться в waiting без курьера, прежний курьер MUST стать available, а факт снятия назначения MUST быть записан в Outbox в той же транзакции. Новый поиск MUST NOT запускаться автоматически.

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

Курьер SHALL получать список своих pending непросроченных `Delivery Offer` с адресами доставки и моментом истечения. Query MUST NOT менять состояние и MUST NOT включать accepted, rejected, withdrawn, просроченные offers и offers других курьеров.

#### Scenario: Список актуальных offers
**ID:** AC-333

- **WHEN** курьер с pending непросроченным offer запрашивает доступные offers
- **THEN** возвращён этот offer с pickup и dropoff адресами и моментом истечения

#### Scenario: Закрытые и чужие offers не видны
**ID:** AC-334

- **WHEN** у курьера есть просроченный и отклонённый offers, а у другого курьера — pending offer
- **THEN** возвращённый курьеру список пуст
