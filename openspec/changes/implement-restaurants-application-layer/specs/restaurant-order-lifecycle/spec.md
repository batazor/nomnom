# Spec Delta

## Purpose

Определяет lifecycle aggregate `Restaurant Order` в `restaurants`: получение состава от `orders`, Acceptance или Rejection, Kitchen Queue, Preparation, Assembly, Ready for Pickup, задержки, неудачу и отмену по стадиям.

## ADDED Requirements

### Requirement: Команды Restaurant Order авторизуются по ресторану
**ID:** FR-220

Команды сотрудников кухни SHALL требовать permission над рестораном заказа, выводимую из relation `staff`; команды fulfillment process `orders` SHALL требовать permission `service` identity над тем же рестораном. Authorization MUST выполняться до доменной операции; отказ или ошибка MUST оставлять заказ и Outbox без изменений.

#### Scenario: Сотрудник другого ресторана
**ID:** AC-240

- **WHEN** subject без права над рестораном пытается принять его Restaurant Order
- **THEN** операция отклоняется как permission denied и State заказа не меняется

#### Scenario: Заказ другого ресторана не раскрывается
**ID:** AC-241

- **WHEN** сотрудник ресторана A адресует команду идентификатору Restaurant Order ресторана B
- **THEN** операция отклоняется как not found, не раскрывая State чужого заказа

### Requirement: Receive Order Request идемпотентно создаёт Restaurant Order
**ID:** FR-221

Receive Order Request SHALL создавать `Restaurant Order` в State `RECEIVED` для существующего ресторана. `orderId` MUST быть уникален: повтор с тем же составом возвращает существующий заказ, а повтор с другим рестораном или составом MUST отклоняться как already exists. Состав MUST быть непустым, количество — положительным целым, а Menu Item с одинаковыми options MUST NOT повторяться.

#### Scenario: Первый запрос
**ID:** AC-242

- **WHEN** `orders` передаёт состав нового заказа
- **THEN** создаётся Restaurant Order в `RECEIVED` и возвращается его идентификатор

#### Scenario: Повтор запроса
**ID:** AC-243

- **WHEN** тот же запрос с тем же `orderId` повторяется после потери ответа
- **THEN** возвращается существующий Restaurant Order и второй заказ не создаётся

#### Scenario: Конфликтующий повтор
**ID:** AC-244

- **WHEN** запрос с уже известным `orderId` содержит другой состав
- **THEN** операция отклоняется как already exists

#### Scenario: Некорректный состав
**ID:** AC-245

- **WHEN** состав пуст, количество не положительное целое или позиция повторяется с теми же options
- **THEN** операция отклоняется как invalid Restaurant Order и заказ не создаётся

### Requirement: Accept Order принимает заказ и ставит его в Kitchen Queue
**ID:** FR-222

Accept Order SHALL принимать только заказ в `RECEIVED`, если ресторан открыт, удовлетворяет `RestaurantAcceptingOrders` и все позиции существуют в меню и удовлетворяют `MenuItemAvailable` на момент принятия. В той же Unit of Work заказ MUST перейти в `QUEUED`, получить `accepted_at`, `queued_at`, `expected_ready_at`, а в Outbox MUST быть записан только `RestaurantOrderAccepted`.

#### Scenario: Принятие открытым рестораном
**ID:** AC-246

- **WHEN** сотрудник принимает полученный заказ открытого ресторана с доступными позициями
- **THEN** заказ в `QUEUED`, Preparation не начата, в Outbox записан ровно один `RestaurantOrderAccepted`

#### Scenario: Ресторан закрыт или позиция недоступна
**ID:** AC-247

- **WHEN** ресторан закрыт, не принимает заказы по расписанию или capacity, либо позиция отсутствует или недоступна
- **THEN** операция отклоняется как failed precondition с перечнем причин, заказ остаётся в `RECEIVED`, Outbox не меняется

#### Scenario: Повтор принятия
**ID:** AC-248

- **WHEN** принимается заказ, уже находящийся в Kitchen Queue
- **THEN** операция отклоняется как invalid transition без нового бизнес-факта

### Requirement: Reject Order отклоняет только полученный заказ
**ID:** FR-223

Reject Order SHALL переводить заказ из `RECEIVED` в `REJECTED`, сохраняя причину при наличии, и MUST атомарно записывать `RestaurantOrderRejected` со стадией до Acceptance. Отказ после Acceptance MUST отклоняться как invalid transition до отдельного продуктового решения.

#### Scenario: Отклонение полученного заказа
**ID:** AC-249

- **WHEN** сотрудник отклоняет полученный заказ с причиной
- **THEN** заказ в `REJECTED`, причина сохранена, в Outbox записан `RestaurantOrderRejected`

#### Scenario: Отклонение после Acceptance
**ID:** AC-250

- **WHEN** сотрудник отклоняет заказ в Kitchen Queue
- **THEN** операция отклоняется как invalid transition и заказ остаётся в очереди

### Requirement: Queue Restaurant Order ставит принятый заказ в очередь
**ID:** FR-224

Queue Restaurant Order SHALL переводить заказ из `ACCEPTED` в `QUEUED` с `queued_at` как локальный факт без публикации. Повтор для заказа, уже находящегося в `QUEUED`, SHALL быть успешным без изменения состояния.

#### Scenario: Отложенная постановка в очередь
**ID:** AC-251

- **WHEN** система ставит в очередь принятый заказ
- **THEN** заказ в `QUEUED`, `queued_at` установлен, Outbox не меняется

#### Scenario: Повторная постановка
**ID:** AC-252

- **WHEN** система ставит в очередь заказ, уже находящийся в `QUEUED`
- **THEN** операция успешна без изменения версии заказа

### Requirement: Start Preparation начинает приготовление заказа из очереди
**ID:** FR-225

Start Preparation SHALL переводить заказ из `QUEUED` в `PREPARING` с `preparation_started_at` и MUST атомарно записывать `PreparationStarted`. Поведение повтора для заказа в `PREPARING` MUST задаваться явной политикой composition root: успешный no-op без бизнес-факта либо отказ как already in state.

#### Scenario: Кухня начинает приготовление
**ID:** AC-253

- **WHEN** сотрудник кухни начинает приготовление заказа из очереди
- **THEN** заказ в `PREPARING` и в Outbox записан `PreparationStarted`

#### Scenario: Повтор при политике no-op
**ID:** AC-254

- **WHEN** политика повтора — no-op и приготовление уже начато
- **THEN** возвращается текущее состояние без нового `PreparationStarted`

#### Scenario: Повтор при строгой политике
**ID:** AC-255

- **WHEN** политика повтора — отказ и приготовление уже начато
- **THEN** операция отклоняется как already in state

### Requirement: Mark Assembled подтверждает готовность всех позиций
**ID:** FR-226

Mark Assembled SHALL переводить заказ из `PREPARING` в `ASSEMBLED` как локальный факт без публикации. Без item-level model команда является утверждением кухни, что все required items приготовлены и собраны.

#### Scenario: Сборка заказа
**ID:** AC-256

- **WHEN** сотрудник кухни отмечает сборку готовящегося заказа
- **THEN** заказ в `ASSEMBLED` и Outbox не меняется

### Requirement: Mark Ready for Pickup допустим только после Assembly
**ID:** FR-227

Mark Ready for Pickup SHALL переводить заказ из `ASSEMBLED` в `READY` с `ready_at` и MUST атомарно записывать `RestaurantOrderReady`. Из `PREPARING` команда MUST отклоняться как invalid transition. Повтор для `READY` MUST следовать той же явной политике повтора, что и Start Preparation.

#### Scenario: Заказ готов
**ID:** AC-257

- **WHEN** сотрудник кухни отмечает готовность собранного заказа
- **THEN** заказ в `READY` и в Outbox записан `RestaurantOrderReady`

#### Scenario: Готовность раньше Assembly
**ID:** AC-258

- **WHEN** сотрудник кухни отмечает готовность заказа, который ещё не собран
- **THEN** операция отклоняется как invalid transition и заказ не готов к Pickup

### Requirement: Report Preparation Delay переносит ожидаемое время готовности
**ID:** FR-228

Report Preparation Delay SHALL для заказа в `QUEUED`, `PREPARING` или `ASSEMBLED` менять `expected_ready_at` без изменения State и MUST атомарно записывать `PreparationDelayed` с прежним и новым временем. Новое время MUST быть позже текущего `expected_ready_at` и позже текущего момента; иначе операция отклоняется guard failure `not_later`.

#### Scenario: Задержка приготовления
**ID:** AC-259

- **WHEN** кухня сообщает более позднее ожидаемое время готовности
- **THEN** `expected_ready_at` изменён, State тот же, в Outbox записан `PreparationDelayed`

#### Scenario: Время не позже текущего
**ID:** AC-260

- **WHEN** новое время равно текущему `expected_ready_at` или раньше него
- **THEN** операция отклоняется guard failure `not_later` и заказ не меняется

### Requirement: Report Preparation Failure завершает начатое приготовление неудачей
**ID:** FR-229

Report Preparation Failure SHALL переводить заказ из `PREPARING` или `ASSEMBLED` в `FAILED` с обязательной причиной и MUST атомарно записывать факт неудачи приготовления.

#### Scenario: Приготовление невозможно завершить
**ID:** AC-261

- **WHEN** кухня сообщает неудачу готовящегося заказа с причиной
- **THEN** заказ в `FAILED`, причина сохранена, в Outbox записан факт неудачи

#### Scenario: Неудача до начала Preparation
**ID:** AC-262

- **WHEN** неудача сообщается для заказа в Kitchen Queue
- **THEN** операция отклоняется как invalid transition

### Requirement: Cancel Restaurant Order возвращает бизнес-результат по стадии
**ID:** FR-230

Cancel Restaurant Order SHALL отменять заказ в `RECEIVED`, `ACCEPTED` или `QUEUED`; для `PREPARING` и `ASSEMBLED` решение MUST задаваться явной политикой. Ответ SHALL быть бизнес-результатом `Cancelled{stage}` либо `CancellationRejected{reason}`, а не ошибкой. При изменении State в Outbox MUST быть записан `RestaurantOrderCancelled`; повтор для `CANCELLED` MUST возвращать сохранённую стадию без нового факта.

#### Scenario: Отмена до Preparation
**ID:** AC-263

- **WHEN** `orders` отменяет заказ в Kitchen Queue
- **THEN** ответ `Cancelled{QUEUED}`, заказ в `CANCELLED`, в Outbox записан `RestaurantOrderCancelled`

#### Scenario: Повтор отмены
**ID:** AC-264

- **WHEN** `orders` повторяет отмену уже отменённого заказа
- **THEN** ответ `Cancelled` с исходной стадией и без нового бизнес-факта

#### Scenario: Отмена готового заказа
**ID:** AC-265

- **WHEN** `orders` отменяет заказ в `READY`
- **THEN** ответ `CancellationRejected{already_ready}`, State не меняется

#### Scenario: Отмена во время Preparation запрещена политикой
**ID:** AC-266

- **WHEN** политика запрещает отмену во время Preparation и заказ в `PREPARING`
- **THEN** ответ `CancellationRejected{cancellation_during_preparation_not_allowed}`, State не меняется

#### Scenario: Отмена во время Preparation разрешена политикой
**ID:** AC-267

- **WHEN** политика разрешает отмену во время Preparation и заказ в `ASSEMBLED`
- **THEN** ответ `Cancelled{ASSEMBLED}` и факт отмены отмечает, что Preparation была начата

### Requirement: Terminal State Restaurant Order не меняется
**ID:** FR-231

После `READY`, `REJECTED`, `CANCELLED` или `FAILED` изменяющие переходы MUST отклоняться как invalid transition или already in state. Каждая пара State × trigger MUST иметь явно определённый исход transition table.

#### Scenario: Попытка начать приготовление отменённого заказа
**ID:** AC-268

- **WHEN** сотрудник кухни начинает приготовление отменённого заказа
- **THEN** операция отклоняется как invalid transition и State остаётся `CANCELLED`

### Requirement: Get Restaurant Order возвращает состав, State и timeline
**ID:** FR-232

Get Restaurant Order SHALL возвращать авторизованному сотруднику ресторана или внутреннему actor состав, State, timeline и сохранённые причины одного заказа без изменения состояния. Заказ другого ресторана MUST отклоняться как not found.

#### Scenario: Просмотр заказа
**ID:** AC-269

- **WHEN** сотрудник запрашивает принятый заказ своего ресторана
- **THEN** возвращаются позиции, State `QUEUED`, `accepted_at`, `queued_at` и `expected_ready_at`
