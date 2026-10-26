# Spec Delta

## ADDED Requirements

### Requirement: Hire Cook
**ID:** FR-614

`restaurants` SHALL предоставлять команду Hire Cook и RPC `HireCook(restaurant_id, name)`: создаёт Cook ресторана вне линии и возвращает его `cook_id`. Имя MUST быть непустым после trim и не длиннее 64 символов, иначе `INVALID_ARGUMENT`. Команды над Cook Pool MUST требовать `restaurant#manage_cooks` (owner и staff) и проверенной identity; отказ — `PERMISSION_DENIED` без изменения состояния.

#### Scenario: Сотрудник нанимает повара
**ID:** AC-775

- **WHEN** сотрудник ресторана вызывает `HireCook` с именем «Айгуль»
- **THEN** создан Cook «Айгуль» вне линии, он входит в Cook Pool ресторана

#### Scenario: Пустое имя
**ID:** AC-776

- **WHEN** сотрудник вызывает `HireCook` с именем из пробелов
- **THEN** ответ `INVALID_ARGUMENT`, Cook не создан

#### Scenario: Посторонний
**ID:** AC-777

- **WHEN** пользователь без relation `owner` или `staff` ресторана вызывает `HireCook`
- **THEN** ответ `PERMISSION_DENIED`, Cook Pool не меняется

### Requirement: Линия поваров
**ID:** FR-615

`restaurants` SHALL предоставлять Put Cook On Line, Take Cook Off Line и Retire Cook с правом `restaurant#manage_cooks`. Повтор действующего значения MUST быть no-op. Take Cook Off Line MUST отклоняться как `CookBusy`, пока Cook держит заказ в `PREPARING` или `ASSEMBLED`. Retire Cook MUST отклоняться как `CookOnLine` для Cook на линии; выбывший Cook MUST NOT входить в Cook Pool, назначаться заказам и возвращаться на линию.

#### Scenario: Повар встаёт на линию
**ID:** AC-778

- **WHEN** сотрудник ставит на линию Cook вне линии
- **THEN** Cook на линии и может получить следующий заказ при Start Preparation

#### Scenario: Занятого повара нельзя снять
**ID:** AC-779

- **WHEN** сотрудник снимает с линии Cook, у которого заказ в `PREPARING`
- **THEN** ответ `CookBusy`, Cook остаётся на линии

#### Scenario: Повара на линии нельзя убрать из пула
**ID:** AC-780

- **WHEN** сотрудник вызывает Retire Cook для Cook на линии
- **THEN** ответ `CookOnLine`, Cook остаётся в пуле

### Requirement: Get Cook Pool
**ID:** FR-616

`restaurants` SHALL предоставлять query Get Cook Pool и RPC `GetCookPool(restaurant_id)` с правом `restaurant#view_orders`: `size` — невыбывшие Cook, `on_line`, `busy` — Cook с заказом в `PREPARING` или `ASSEMBLED`, `queued` — заказы ресторана в `QUEUED`, и список Cook с именем, признаком линии и `restaurant_order_id` текущего заказа. Query MUST NOT менять состояние.

#### Scenario: Загрузка кухни
**ID:** AC-781

- **GIVEN** в пуле 5 Cook, 3 на линии, 2 готовят, в очереди 4 заказа
- **WHEN** сотрудник вызывает `GetCookPool`
- **THEN** ответ `size = 5`, `on_line = 3`, `busy = 2`, `queued = 4` и пять Cook, у двух — текущий заказ

### Requirement: Cook держит один активный заказ
**ID:** FR-617

Cook SHALL считаться занятым, пока Restaurant Order с его `cook_id` находится в `PREPARING` или `ASSEMBLED`; переход заказа в `READY`, `FAILED` или `CANCELLED` MUST освобождать Cook без отдельной команды. Хранилище MUST гарантировать не больше одного такого заказа на Cook. `cook_id` SHALL сохраняться в заказе после завершения.

#### Scenario: Повар освобождается на READY
**ID:** AC-784

- **GIVEN** Cook «Олег» готовит заказ, а второй заказ ждёт в `QUEUED`
- **WHEN** кухня отмечает первый заказ `READY`
- **THEN** «Олег» свободен, у первого заказа `cook_id` «Олега», а Start Preparation второго заказа назначает «Олега»

#### Scenario: Одновременный старт на последнего свободного повара
**ID:** AC-785

- **GIVEN** на линии один свободный Cook и два заказа в `QUEUED`
- **WHEN** Start Preparation обоих заказов выполняется одновременно
- **THEN** один заказ в `PREPARING` с этим Cook, второй получает `NoCookAvailable` и остаётся в `QUEUED`

### Requirement: Simulator кухни работает с Cook Pool
**ID:** FR-618

Simulator `restaurants` SHALL выполнять запланированный Start Preparation, только когда в Cook Pool ресторана есть свободный Cook на линии; при `NoCookAvailable` шаг MUST повторяться на следующем тике. После старта оставшиеся шаги SHALL планироваться от фактического момента старта. Simulator MUST NOT менять состав Cook Pool и линию.

#### Scenario: Пустая линия
**ID:** AC-786

- **GIVEN** Simulator включён, у ресторана нет Cook на линии, принятый заказ в `QUEUED`
- **WHEN** проходит `SIMULATOR__MAX_DELAY`
- **THEN** заказ остаётся в `QUEUED`; после того как сотрудник ставит Cook на линию, Simulator начинает Preparation с этим Cook
