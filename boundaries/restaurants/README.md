# Restaurants

## Назначение

Контекст `restaurants` отвечает за работу ресторанов, предложение блюд и выполнение рестораном принятого заказа.

## Язык

TypeScript.

Стратегия unit, BDD acceptance, integration и system E2E проверок описана в [общей документации тестирования](../../docs/testing.md).

## Данные и состояния

Контекст владеет данными ресторанов, меню, доступностью блюд, состоянием принятия заказа рестораном и состояниями приготовления. Кухонные сценарии `Restaurant Order → Kitchen Queue → Preparation → Assembly → Ready for Pickup`, доменная модель приготовления, состояния и бизнес-сценарии детализированы в [Kitchen](../../docs/kitchen.md).

## Границы ответственности

`restaurants` принимает или отклоняет заказ со стороны ресторана и управляет приготовлением. Он не управляет общим жизненным циклом заказа, назначением курьеров, доставкой или платёжными операциями; результаты передаются в `orders` для отражения в состоянии заказа.

## Use Cases

Статус реализации: application handlers всех сценариев, кроме **Start / Complete Preparation Item** и **Reject After Acceptance**, реализованы change [`implement-restaurants-application-layer`](../../openspec/changes/implement-restaurants-application-layer/) поверх портов; хранилище — MongoDB, брокер не подключён, к transport подключена часть handlers (см. [Статус реализации](#статус-реализации)). Новые handlers, контракты и adapters по-прежнему создаются только вместе с SDD change; пустые программные scaffolds не используются.

### Commands

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Register Restaurant** | Оператор платформы или владелец ресторана | Создать ресторан как управляемый ресурс | Создан `Restaurant`; onboarding, verification и billing не входят в сценарий | Authoritative authorization в `restaurants` |
| **Update Restaurant** | Авторизованный управляющий ресторана | Изменить основные данные ресторана | Разрешённые данные `Restaurant` обновлены | Authoritative authorization в `restaurants` |
| **Open Restaurant** | Авторизованный управляющий ресторана | Разрешить приём новых заказов | `Restaurant` открыт для новых `Restaurant Order` | Может опубликовать бизнес-факт через Outbox |
| **Close Restaurant** | Авторизованный управляющий ресторана | Прекратить приём новых заказов | `Restaurant` закрыт для новых `Restaurant Order`; уже принятые заказы не отменяются автоматически | Может опубликовать бизнес-факт через Outbox |
| **Assign Staff** | Пользователь с `restaurant#manage` (владелец) | Дать пользователю доступ персонала | User id добавлен в `Restaurant.staff`; `TOUCH restaurant#staff` до сохранения | Повтор — no-op без записи; см. [Relationships](#relationships-и-reconciliation) |
| **Revoke Staff** | Пользователь с `restaurant#manage` (владелец) | Отозвать доступ персонала | User id удалён из `Restaurant.staff`; `DELETE restaurant#staff` до сохранения | Повтор — no-op без записи; см. [Relationships](#relationships-и-reconciliation) |
| **Create Menu** | Авторизованный управляющий ресторана | Создать меню ресторана | Создано принадлежащее ресторану меню | Authoritative authorization в `restaurants` |
| **Add Menu Item** | Авторизованный управляющий ресторана | Добавить блюдо в меню | Создан новый `Menu Item` с исходными данными, ценой и доступностью | Authoritative authorization в `restaurants` |
| **Update Menu Item** | Авторизованный управляющий ресторана | Изменить название, описание, цену, доступность, options/modifiers или иные основные данные блюда | `Menu Item` обновлён в пределах инвариантов меню | Не изменяет snapshot уже оформленного `Order` |
| **Set Menu Item Availability** | Авторизованный управляющий ресторана | Быстро изменить возможность заказать блюдо | Доступность `Menu Item` изменена без редактирования остальных данных меню | Может опубликовать бизнес-факт через Outbox |
| **Receive Order Request** | Fulfillment process `orders` | Передать ресторану состав заказа для решения и приготовления | Создан идемпотентный локальный `Restaurant Order` в состоянии ожидания решения | Вызывается Temporal Activity через internal gateway и consumer `OrderPlaced` (`restaurants-consumer`, Inbox); оба пути идемпотентны по `order_id`, повтор не создаёт дубликат |
| **Accept Order** | Авторизованный сотрудник ресторана | Подтвердить возможность приготовить весь `Restaurant Order` | `Restaurant Order` принят и поставлен в Kitchen Queue, результат доступен `orders` | Проверяет открытость и доступность позиций; `expected_ready_at` называет кухня (позже момента принятия) или считает Ready Time Estimator ([FR-609](../../openspec/changes/add-fulfillment-eta/)); публикует `RestaurantOrderAccepted` с ним через Outbox |
| **Reject Order** | Авторизованный сотрудник ресторана | Отказаться от приготовления `Restaurant Order` | `Restaurant Order` отклонён, причина сохранена при наличии и доступна `orders` | Публикует `RestaurantOrderRejected` через Outbox |
| **Queue Restaurant Order** | Система `restaurants` | Поставить принятый заказ в Kitchen Queue | Заказ занял место в очереди по согласованной политике; приготовление не начато | В MVP — в одной UoW с Accept Order; локальный факт, не публикуется |
| **Start Preparation** | Авторизованный сотрудник кухни | Зафиксировать начало Preparation заказа из очереди | `Restaurant Order` готовится; повтор — по политике `AlreadyInState` | Публикует `PreparationStarted` через Outbox; slice `start-preparation` |
| **Start / Complete Preparation Item** | Авторизованный сотрудник станции | Отметить начало и завершение приготовления позиции | Позиция `PREPARING` / `READY` | Только при item-level model; локальные факты |
| **Mark Assembled** | Авторизованный сотрудник кухни | Подтвердить, что все required items готовы и собраны | `Restaurant Order` собран | Guard готовности позиций; локальный факт |
| **Mark Ready for Pickup** | Авторизованный сотрудник кухни | Сообщить о готовности заказа к Pickup | `Restaurant Order` готов к выдаче; допустимо только после Assembly | Публикует `RestaurantOrderReady` через Outbox; slice `mark-ready-for-pickup` |
| **Report Preparation Delay** | Авторизованный сотрудник кухни | Сообщить, что заказ будет готов позже | `expected_ready_at` изменён, State не меняется | Публикует `PreparationDelayed` через Outbox |
| **Report Preparation Failure** | Авторизованный сотрудник кухни | Сообщить, что начатое приготовление невозможно завершить | `Restaurant Order` в `FAILED` с причиной | Публикует `PreparationFailed` через Outbox |
| **Cancel Restaurant Order** | Fulfillment process `orders` | Запросить прекращение работы над `Restaurant Order` | Синхронный ответ по стадии: отменено либо отказ с причиной | Temporal Activity → internal gateway → `restaurants`; `RestaurantOrderCancelled` через Outbox при изменении State; не завершает общий `Order` |
| **Set Simulator Enabled** | Оператор платформы | Включить или выключить Demo Simulator кухни | Состояние сохранено и переживает рестарт | `platform#operate_simulator`; slice `simulator` ([FR-604](../../openspec/changes/add-demo-simulator/)) |
| **Simulate Kitchen** | Demo Simulator от имени владельца ресторана | Продвигать заказы без участника, пока Simulator включён | Через случайную задержку вызван настоящий kitchen use case по весам FR-605; при принятии вся готовка планируется заранее, срок — запланированный `READY` (FR-609) | Фоновый `PeriodicLoop`; slice `simulator`, [README](slices/simulator/applications/simulate-kitchen/README.md) |
| **Reject After Acceptance** | Авторизованный сотрудник ресторана | Отказаться от принятого заказа до начала Preparation | `Restaurant Order` отклонён | Отдельное продуктовое решение; в MVP не разрешено |

### Queries

| Use Case | Actor | Intent | Result | Ключевые зависимости и взаимодействия |
| --- | --- | --- | --- | --- |
| **Get Restaurant** | Клиент или внутренний actor | Получить публичное представление ресторана | Возвращены доступные публичные данные ресторана без изменения состояния | Query model `restaurants` |
| **List Restaurants** | Клиент | Найти доступные для выбора рестораны | Возвращён список доступных ресторанов без изменения состояния | Query model `restaurants`; критерии доступности уточняются в SDD |
| **List Owned Restaurants** | Владелец ресторана | Найти рестораны, которыми он управляет | Возвращены открытые и закрытые рестораны владельца без изменения состояния | Query model `restaurants` по `ownerId`; `restaurant#manage = owner`, поэтому фильтр и есть authorization |
| **Get Menu** | Клиент или внутренний actor | Получить актуальное меню ресторана | Возвращены актуальные позиции, цены, options/modifiers и доступность | Query model `restaurants`; это не snapshot оформленного заказа |
| **List Incoming Orders** | Авторизованный сотрудник ресторана | Увидеть новые заказы, ожидающие решения кухни | Заказы в `RECEIVED` по возрастанию `received_at` с позициями и версией; принятый заказ уходит из списка | Read model kitchen board |
| **List Active Restaurant Orders** | Авторизованный сотрудник ресторана | Увидеть всю текущую работу кухни одним списком | Заказы в `RECEIVED`, `QUEUED`, `PREPARING`, `ASSEMBLED` и `READY` по возрастанию `received_at` с состоянием, позициями и версией | Read model kitchen board; порядок очереди — Get Kitchen Queue |
| **Get Kitchen Queue** | Авторизованный сотрудник ресторана | Увидеть заказы, ожидающие Preparation | Заказы в очереди в рекомендованном порядке | Projection Kitchen Queue, не aggregate |
| **Get Restaurant Order** | Авторизованный сотрудник ресторана или внутренний actor | Получить состав, State и timeline заказа | Представление одного `Restaurant Order` | Read model `restaurants` |
| **List Active Preparations** | Авторизованный сотрудник ресторана | Увидеть заказы в работе кухни | Готовящиеся и собранные заказы с задержками | Read model kitchen board |
| **Get Simulator** | Оператор платформы | Узнать, включён ли Demo Simulator | `enabled`; без сохранённого состояния — `false` | `platform#operate_simulator`; slice `simulator` |
| **List Ready Orders** | Авторизованный сотрудник ресторана | Увидеть заказы, ожидающие передачи курьеру | Готовые заказы и информационные данные курьера | Read model kitchen board; данные курьера — projection событий `delivery` |

Команды кухни требуют permission над `restaurant`, выводимую из relation `staff`; роли cook/manager не моделируются. Сценарии, политики повтора и отмены по стадиям — в [Kitchen](../../docs/kitchen.md#use-cases).

## Структура слайсов

Слайсы `restaurants` следуют [общей DDD-структуре и правилам зависимостей](../README.md#организация-кода-внутри-контекста): `slices/restaurant` владеет рестораном и правилами приёма заказов, `slices/menu` — меню и доступностью позиций, `slices/restaurantorder` владеет жизненным циклом `Restaurant Order`, включая его роль `Kitchen Order` на кухне, а `slices/simulator` — Demo Simulator кухни (состояние вкл/выкл и фоновые действия от имени владельца ресторана). Use cases размещаются в `slices/<slice>/applications/<usecase>/`, shared packages — в `pkg/`. Доменные модели не зависят от транспорта, хранилища или Awilix.

## gRPC-адаптер

`restaurants` владеет API `nomnom.restaurants.v1` и публикует его как `buf.build/nomnom/restaurants`. Исходные messages находятся в `infrastructure/grpc/nomnom/restaurants/v1/restaurants.proto`, service и RPC — в соседнем `service.proto`; корень Buf-модуля — `infrastructure/grpc`.

Адаптер использует сгенерированные TypeScript messages и service descriptors с Connect runtime и преобразует request messages в прикладные команды или запросы, а результаты — в response messages. Protobuf-типы остаются в `infrastructure` и не используются моделями `Restaurant`, `Menu Item`, `Restaurant Order` или их handlers. Клиенты других контекстов получают схему только из BSR по закреплённому commit ref.

Маппинг ошибок API:

| Ошибка `restaurants`                                               | gRPC code             | `ErrorInfo.reason`                  |
| ------------------------------------------------------------------ | --------------------- | ----------------------------------- |
| `Restaurant`, `Menu Item` или `Kitchen Order` не найден            | `NOT_FOUND`           | `RESOURCE_NOT_FOUND`                |
| Некорректный состав `Kitchen Order`                                | `INVALID_ARGUMENT`    | `INVALID_KITCHEN_ORDER`             |
| Ресторан закрыт, позиция недоступна или переход состояния запрещён | `FAILED_PRECONDITION` | `KITCHEN_ORDER_PRECONDITION_FAILED` |
| `Kitchen Order` уже существует                                     | `ALREADY_EXISTS`      | `KITCHEN_ORDER_ALREADY_EXISTS`      |
| Неожиданная внутренняя ошибка                                      | `INTERNAL`            | `INTERNAL_ERROR`                    |
| Внешняя зависимость недоступна                                     | `UNAVAILABLE`         | `DEPENDENCY_UNAVAILABLE`            |

Термин и reasons `KITCHEN_ORDER_*` в таблице — legacy-имена; до создания `.proto` они приводятся к `Restaurant Order` (`RESTAURANT_ORDER_*`). Отказ в Cancel Restaurant Order по стадии — бизнес-результат ответа, а не ошибка transport ([Kitchen](../../docs/kitchen.md#cancel-restaurant-order)).

Transport adapter маппит application errors в gRPC status codes по стабильному `code` ошибки: валидация — `INVALID_ARGUMENT`, не найдено — `NOT_FOUND`, конфликтующий повтор — `ALREADY_EXISTS`, запрещённый переход — `FAILED_PRECONDITION`, нет прав — `PERMISSION_DENIED`, authorization недоступна — `UNAVAILABLE`. Добавление структурированных `google.rpc.ErrorInfo` с domain `nomnom.restaurants.v1` остаётся частью расширения error contract. Политика контрактов, генерации и совместимости описана в [ADR 7](../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md).

RPC, подключённые в `cmd/restaurants` (native gRPC/HTTP2 на бизнес-порту, общий lifecycle):

| RPC | Handler | Результат |
| --- | --- | --- |
| `GetRestaurant` | `get-restaurant` | Публичные данные и pickup `address`; без identity |
| `GetMenu` | `get-menu` | Текущее меню: название, описание, цена, доступность; без identity; нет меню — `NOT_FOUND` |
| `ListRestaurants` | `list-restaurants` | Страница ресторанов и `next_page_token` (пустой на последней); без identity; по умолчанию только открытые, `include_closed` — все; `page_size` 1..100 (0 — 50), иначе `INVALID_ARGUMENT` |
| `ListOwnedRestaurants` | `list-owned-restaurants` | Открытые и закрытые рестораны вызывающего пользователя по возрастанию id, `next_page_token` пустой на последней; нужен `user` subject, service — `PERMISSION_DENIED`; `page_size` как у `ListRestaurants` |
| `ReceiveOrderRequest` | `receive-order-request` | `restaurant_order_id`, `created = false` при повторе по `order_id` |
| `CancelRestaurantOrder` | `cancel-restaurant-order` | `cancelled = true` (включая повтор) или `cancelled = false` + `rejection_reason` |
| `AcceptRestaurantOrder` | `accept-order` | `RestaurantOrder` в `QUEUED` с `expected_ready_at` из запроса или от Ready Time Estimator; срок не позже момента принятия — `INVALID_ARGUMENT`; публикует `RestaurantOrderAccepted` с `expected_ready_at` |
| `RejectRestaurantOrder` | `reject-order` | `RestaurantOrder` в `REJECTED`; публикует `RestaurantOrderRejected` |
| `StartPreparation` | `start-preparation` | `RestaurantOrder` в `PREPARING`; публикует `PreparationStarted`, повтор — без факта |
| `MarkRestaurantOrderAssembled` | `mark-assembled` | `RestaurantOrder` в `ASSEMBLED`; локальный факт, без Outbox |
| `MarkRestaurantOrderReady` | `mark-ready-for-pickup` | `RestaurantOrder` в `READY`; публикует `RestaurantOrderReady`, повтор — без факта |
| `ReportPreparationDelay` | `report-preparation-delay` | `RestaurantOrder` с новым `expected_ready_at`; публикует `PreparationDelayed` |
| `ReportPreparationFailure` | `report-preparation-failure` | `RestaurantOrder` в `FAILED`; публикует `PreparationFailed` |
| `GetKitchenQueue` | `get-kitchen-queue` | `entries[]` в рекомендованном порядке: `restaurant_order` (`RestaurantOrder` в `QUEUED` с `restaurant_order_id`, `order_id`, `expected_ready_at`, `version`), `accepted_at`, `queued_at`; требует `restaurant#operate_kitchen`; заказы в `RECEIVED` не входят |
| `ListIncomingOrders` | `list-incoming-orders` | `orders[]` по возрастанию `received_at`, затем по id: `restaurant_order` (`RestaurantOrder` в `RECEIVED` с `restaurant_order_id`, `order_id`, `version`, без `expected_ready_at`), `items[]` (`menu_item_id`, `quantity`, `options`), `received_at`; требует `restaurant#operate_kitchen`; принятый заказ в ответ не входит |
| `ListActiveRestaurantOrders` | `list-active-restaurant-orders` | `orders[]` по возрастанию `received_at`, затем id: `restaurant_order` (`RestaurantOrder` в `RECEIVED`, `QUEUED`, `PREPARING`, `ASSEMBLED` или `READY` с `expected_ready_at` и `version`), `items`, `received_at`; требует `restaurant#operate_kitchen`; рекомендованный порядок `QUEUED` — `GetKitchenQueue`; клиент — kitchen-экран BFF |
| `RegisterRestaurant` | `register-restaurant` | `Restaurant` (с `address`) и `version`; требует `platform#register_restaurant`, пишет `restaurant#owner`; не идемпотентен |
| `AssignStaff` | `assign-staff` | `restaurant_id`, отсортированный `staff`, `changed = false` при повторе; требует `restaurant#manage`, пишет `restaurant#staff` |
| `RevokeStaff` | `revoke-staff` | `restaurant_id`, отсортированный `staff`, `changed = false` при повторе; требует `restaurant#manage`, удаляет `restaurant#staff` |
| `OpenRestaurant` | `open-restaurant` | `Restaurant`, `version`, `changed = false` при повторе; требует `restaurant#manage`; публикует `RestaurantOpened` |
| `CloseRestaurant` | `close-restaurant` | `Restaurant`, `version`, `changed = false` при повторе; требует `restaurant#manage`; публикует `RestaurantClosed`, принятые заказы не отменяет |
| `CreateMenu` | `create-menu` | `menu_id`, `restaurant_id`, `version`; требует `restaurant#manage_menu`; повтор — `ALREADY_EXISTS`, нет ресторана — `NOT_FOUND` |
| `AddMenuItem` | `add-menu-item` | `menu_id`, `MenuItem`, `version`; требует `restaurant#manage_menu`; `available` по умолчанию `true`; не идемпотентен; без факта |
| `UpdateMenuItem` | `update-menu-item` | `menu_id`, `MenuItem`, `version`, `changed`; незаданные поля не меняются, пустой `description` удаляет описание; смена доступности публикует `MenuItemAvailabilityChanged` |
| `SetMenuItemAvailability` | `set-menu-item-availability` | `menu_id`, `MenuItem`, `version`, `changed = false` при повторе; публикует `MenuItemAvailabilityChanged` |
| `GetSimulator` | `get-simulator` | `enabled`; требует `platform#operate_simulator`, иначе `PERMISSION_DENIED` (FR-604) |
| `SetSimulatorEnabled` | `set-simulator-enabled` | новое `enabled`; требует `platform#operate_simulator`; состояние — документ коллекции `simulator`, без документа Simulator выключен |

Командные RPC ([FR-428](../../openspec/changes/add-fulfillment-command-rpcs/), [FR-446](../../openspec/changes/add-restaurant-admin-rpcs/)) получают `Subject` через `SubjectResolver` (`infrastructure/grpc/subject.ts`). Без конфигурации identity используется `RejectingSubjectResolver`, который отвечает `UNAUTHENTICATED`; с конфигурацией — `ServiceTokenSubjectResolver`: тонкий resolver, который по порядку вызывает `ServiceTokenVerifier` (`infrastructure/grpc/service-token.ts`) и при наличии assertion — `UserAssertionVerifier` (`infrastructure/grpc/user-assertion.ts`), пишет log и audit (см. [Service identity](#service-identity) и [User identity](#user-identity)). Отказ отмены по стадии — бизнес-ответ, а не gRPC-ошибка.

Команды кухни ([FR-480…FR-482](../../openspec/changes/add-kitchen-command-rpcs/)) адресуют заказ парой `restaurant_id` + `restaurant_order_id` и требуют `restaurant#operate_kitchen`; заказ другого ресторана — `NOT_FOUND`, запрещённый переход и повтор (кроме `StartPreparation`/`MarkRestaurantOrderReady`, которые по умолчанию отвечают текущим заказом без факта) — `FAILED_PRECONDITION`. Пока модель расписания и capacity не согласована, Accept Order собран с `UnconstrainedAcceptanceConditions` (решают `Restaurant.isOpen` и `MenuItem.isAvailable`) и `FixedPreparationTimeEstimator`.

`GetKitchenQueue` ([FR-583](../../openspec/changes/close-order-lifecycle-in-kind/)) и `ListIncomingOrders` ([FR-586](../../openspec/changes/close-order-lifecycle-in-kind/)) — queries kitchen board (`infrastructure/grpc/kitchen-board.ts`): тот же `SubjectResolver`, что у команд кухни, проверка `restaurant#operate_kitchen` через порт `KitchenStaffAccess` каждого query, без identity — `UNAUTHENTICATED`, пустой `restaurant_id` — `INVALID_ARGUMENT`. Оба читают `MongoKitchenBoard`. Новый заказ сотрудник находит в `ListIncomingOrders` и отвечает на него `AcceptRestaurantOrder` или `RejectRestaurantOrder`; принятый заказ переходит в `GetKitchenQueue`. Порядок входящих — `received_at` (индекс `restaurant_orders_board_received`); заказы, полученные до записи `received_at`, идут первыми. Порядок очереди задаёт MVP-политика `FifoKitchenQueueOrdering` (`slices/restaurantorder/infrastructure/kitchen-queue/mvp.ts`: FIFO по `accepted_at`, затем по id), пока алгоритм очереди не согласован.

Команды открытости и меню (change [`add-restaurant-menu-command-rpcs`](../../openspec/changes/add-restaurant-menu-command-rpcs/), FR-570, FR-571) вызывает BFF от имени управляющего; они опубликованы в internal gateway route `restaurants-v1-management` (`ops/kind/manifests/gateway/gateway.yaml`). Open/Close Restaurant требуют `restaurant#manage`, команды меню — `restaurant#manage_menu` (владелец или персонал); схема SpiceDB не менялась. Повтор Open/Close, `SetMenuItemAvailability` и `UpdateMenuItem` с текущими значениями успешен с `changed = false` и без записи Outbox; `CreateMenu` повторно — `ALREADY_EXISTS`; `AddMenuItem` не идемпотентен (каждый вызов добавляет блюдо). Невалидные название или цена (`invalid_menu`, `invalid_money`) — `INVALID_ARGUMENT`.

Открытые продуктовые решения команд кухни handlers получают обязательными параметрами без значения по умолчанию; значения задаёт конфигурация основного процесса (`pkg/config`, секция `kitchen`):

| Переменная | Назначение | По умолчанию |
| --- | --- | --- |
| `KITCHEN__PREPARATION_TIME` | Начальный `expected_ready_at` после Acceptance (`FixedPreparationTimeEstimator`); позже сдвигается Report Preparation Delay | `20m` |
| `KITCHEN__REPEAT_POLICY` | Повтор `StartPreparation`/`MarkRestaurantOrderReady`: `succeed-without-event` — текущий заказ без факта, `reject` — `FAILED_PRECONDITION` | `succeed-without-event` |
| `KITCHEN__CANCELLATION_DURING_PREPARATION` | `CancelRestaurantOrder` во время Preparation: `reject` — отказ по стадии, Preparation продолжается; `allow` — отмена | `reject` |

Demo Simulator кухни ([FR-605, FR-606](../../openspec/changes/add-demo-simulator/)) — `PeriodicLoop` основного процесса рядом с reconciler. Проход: выключен — забыть расписание; включён — найти заказы всех ресторанов в `RECEIVED`/`QUEUED`/`PREPARING`/`ASSEMBLED` (`MongoKitchenBoard.listPendingKitchenOrders`, индекс `restaurant_orders_state_received`), заказу в `RECEIVED` назначить срок решения `первое появление + random[MIN_DELAY, MAX_DELAY]` и по сроку вызвать настоящий handler команды кухни с subject `user:<ownerId>` ресторана (проверка `restaurant#operate_kitchen` как у человека). Принимая заказ, Simulator планирует всю готовку (по random-задержке на шаги start → assemble → ready) и передаёт `expected_ready_at` = запланированный `READY` ([FR-609](../../openspec/changes/add-fulfillment-eta/)); дальше шаги идут в запланированные моменты, задержка сдвигает остаток плана к новому `expected_ready_at`. Заказ без плана (рестарт, принят человеком) планируется от текущего момента; если запланированный `READY` позже его `expected_ready_at`, Simulator сообщает задержку до него. План — только в памяти процесса. Ошибка use case пишется в log, запись отбрасывается. Переменные обязательны для `cmd/restaurants`, default нет: пустое или невалидное значение и `MIN_DELAY > MAX_DELAY` останавливают старт.

| Переменная | Назначение | В kind |
| --- | --- | --- |
| `SIMULATOR__INTERVAL` | Пауза между проходами Simulator | `5s` |
| `SIMULATOR__MIN_DELAY` | Нижняя граница случайной задержки действия | `30s` |
| `SIMULATOR__MAX_DELAY` | Верхняя граница случайной задержки действия | `2m` |

### Service identity

Вызывающий сервис доказывает `service` identity projected ServiceAccount token ([ADR 27](../../docs/adr/0027-service-identity-via-projected-serviceaccount-tokens.md), [FR-431, FR-432](../../openspec/changes/add-service-identity/specs/service-identity/spec.md)). `ServiceTokenSubjectResolver` (`jose`) проверяет:

- metadata `authorization: Bearer <jwt>`;
- header: `alg` только `RS256` или `ES256`, `kid` обязателен и есть в JWKS (`alg: none`, HMAC и неизвестный `kid` отклоняются);
- подпись по локальному JWKS из `IDENTITY__JWKS_FILE` — Kubernetes API при проверке не используется;
- `iss` равен `IDENTITY__ISSUER`, `aud` (строка или массив) содержит `IDENTITY__AUDIENCE`;
- `exp`/`nbf` с допуском 30 s;
- `sub` `system:serviceaccount:<namespace>:<name>` есть в allowlist `IDENTITY__SERVICE_ACCOUNTS` → `Subject{type: "service", id: <service-name>}`.

Любой отказ — `UNAUTHENTICATED` с общим сообщением; причина пишется только в log. Что разрешено identity, решают authorization ports use case, а не resolver.

| Переменная | Значение | По умолчанию |
| --- | --- | --- |
| `IDENTITY__ISSUER` | Issuer токенов кластера, например `https://kubernetes.default.svc.cluster.local` | пусто |
| `IDENTITY__AUDIENCE` | Audience этого контекста, в кластере `nomnom.restaurants` | пусто |
| `IDENTITY__JWKS_FILE` | Путь к JWKS кластера (`/openid/v1/jwks`); в chart — `/etc/nomnom/identity/jwks.json` | пусто |
| `IDENTITY__SERVICE_ACCOUNTS` | Allowlist через запятую: `namespace/serviceaccount=service-name`, например `nomnom/orders-worker=orders-worker` | пусто |

Если `IDENTITY__ISSUER`, `IDENTITY__AUDIENCE` или `IDENTITY__JWKS_FILE` пусты, сервер стартует с `RejectingSubjectResolver` и пишет предупреждение (fail closed). Нечитаемый или невалидный JWKS, пустой allowlist при заданном issuer и неверный формат allowlist — ошибка startup. Тесты (`test/identity.test.ts`) проходят по общим векторам `tests/identity/fixtures` с audience векторов `nomnom.payments`.

### User identity

Пользователя передаёт только BFF: user assertion в metadata `x-nomnom-user-assertion` ([ADR 32](../../docs/adr/0032-user-identity-via-bff-signed-assertions.md), [FR-471–FR-473](../../openspec/changes/add-user-identity-assertions/specs/user-identity/spec.md)). `ServiceTokenSubjectResolver` проверяет по порядку:

1. service token, как выше; без него assertion не рассматривается;
2. assertion нет → `Subject{type: "service"}`, как раньше;
3. assertion повторён или пуст, user-настройки не заданы или service не входит в `IDENTITY__USER_DELEGATES` → `UNAUTHENTICATED`;
4. assertion: `alg` только `ES256`, `kid` обязателен и есть в JWKS BFF, `iss` равен `IDENTITY__USER_ISSUER`, `aud` содержит `IDENTITY__AUDIENCE`, `exp`/`nbf` с допуском 30 s, `iat` обязателен и `exp - iat ≤ 60 s`, `sub` непуст и без пробелов по краям, `jti` непуст → `Subject{type: "user", id: <sub>, delegate: <service>}`.

`delegate` нужен только для log и audit. Принятие и отказ пишутся в log с delegate и `jti`, сами токены не пишутся; `jti` не дедуплицируется.

| Переменная | Значение | По умолчанию |
| --- | --- | --- |
| `IDENTITY__USER_ISSUER` | Ожидаемый `iss` assertion, `nomnom-bff` | пусто |
| `IDENTITY__USER_JWKS_FILE` | JWKS BFF (EC P-256); в chart — `/etc/nomnom/user-identity/jwks.json` из `identity.userJwksConfigMap` | пусто |
| `IDENTITY__USER_DELEGATES` | Services через запятую, которым разрешено передавать пользователя, например `bff` | пусто |

Assertions принимаются, только если заданы issuer и JWKS и список делегатов не пуст; иначе любой вызов с assertion — `UNAUTHENTICATED`, вызовы без него работают как раньше. Нечитаемый JWKS BFF или ключ не EC P-256 — ошибка startup. Тесты (`test/user-identity.test.ts`) проходят по `tests/identity/fixtures/user-vectors.json` с часами, зафиксированными на `now` векторов.

### Authorization: SpiceDB

Authorization ports слайсов (`applications/access-control.ts`; у `restaurantorder` — узкие `KitchenStaffAccess`, `FulfillmentCoordinatorAccess`, `RestaurantOrderViewAccess`) реализует `SpiceDbAccessControl` (`infrastructure/spicedb/access-control.ts`) поверх клиента `infrastructure/spicedb/client.ts` (интерфейсы вызовов, фабрика `@authzed/authzed-node`, deadline, consistency; его же используют writer relationships и reconciler) по нормативной таблице [design.md](../../openspec/changes/add-spicedb-authorization/design.md) и schema [`authorization/schema.zed`](../../authorization/schema.zed) ([FR-435](../../openspec/changes/add-spicedb-authorization/specs/spicedb-authorization/spec.md)):

| Порт | SpiceDB check |
| --- | --- |
| `canRegisterRestaurant` | `platform:nomnom#register_restaurant` |
| `canManageRestaurant(id)` | `restaurant:<id>#manage` |
| `canManageMenu(id)` | `restaurant:<id>#manage_menu` |
| `canOperateKitchen(id)` | `restaurant:<id>#operate_kitchen` |
| `canCoordinateFulfillment(id)` | `platform:nomnom#coordinate_fulfillment` (platform capability, `id` не участвует) |
| `canViewRestaurantOrders(id)` | `restaurant:<id>#view_orders` |

Subject `user` → `user:<id>`, `service` → `service:<id>`. Consistency — см. [Authorization revisions](#authorization-revisions-zedtokens). Разрешает только `PERMISSIONSHIP_HAS_PERMISSION`; `NO_PERMISSION` и conditional — отказ (`PERMISSION_DENIED`). Ошибка, недоступность или превышение `SPICEDB__TIMEOUT` — `AuthorizationUnavailableError` (fail closed, gRPC `UNAVAILABLE`, AC-466). Grants `service` identities (`platform:nomnom#fulfillment@service:orders-worker`) принадлежат развёртыванию (`authorization/bootstrap.yaml`), а не коду; связи `restaurant#owner`/`staff` пишут use cases ресторана (см. [Relationships и reconciliation](#relationships-и-reconciliation)).

| Переменная | Значение | По умолчанию |
| --- | --- | --- |
| `SPICEDB__ENDPOINT` | gRPC endpoint `host:port`, например `spicedb.nomnom-authz.svc.cluster.local:50051` | пусто |
| `SPICEDB__PRESHARED_KEY` | Preshared key (bearer); в кластере — из Secret | пусто |
| `SPICEDB__INSECURE` | `true` — plaintext gRPC без TLS, только kind и локально | `false` |
| `SPICEDB__TIMEOUT` | Deadline одной проверки (`500ms`, `2s`, `1m`) | `2s` |

Пустой `SPICEDB__ENDPOINT` — ошибка startup: процесс без SpiceDB не стартует. Endpoint без preshared key, неверный `SPICEDB__INSECURE` или `SPICEDB__TIMEOUT` — ошибка startup.

Unit-тесты маппинга и fail closed — `test/spicedb-access-control.test.ts` (fake клиент). Integration-тесты `test/spicedb-integration.test.ts` идут в `npm test` на SpiceDB testcontainer с `authorization/bootstrap.yaml` (ключ `devkey`, plaintext).

### Authorization revisions (ZedTokens)

Change [`add-authorization-revisions`](../../openspec/changes/add-authorization-revisions/) (FR-447, FR-448). `touchOwner`, `touchStaff` и `deleteStaff` возвращают ZedToken записи (`writtenAt.token`); Register Restaurant, Assign Staff и Revoke Staff сохраняют его в техническом поле snapshot `authorizationRevision` тем же save (повтор без записи revision не меняет; старые snapshots загружаются без него). Reconciler при `TOUCH` missing связи тоже сохраняет revision в ресторан; удаление orphan revision не меняет. `SpiceDbAccessControl` читает revision через порт `RevisionReader` (в `cmd/restaurants` — тот же `MongoRestaurantRepository`): проверки `restaurant:<id>` идут с `at_least_as_fresh` по сохранённому токену, без revision — `minimize_latency`; проверки `platform:nomnom` всегда `minimize_latency`. Ошибка чтения revision — `AuthorizationUnavailableError` (fail closed). Integration-тесты проверяют адаптер без принудительной full consistency: персонал получает `operate_kitchen` сразу после Assign Staff и теряет сразу после Revoke Staff (AC-482), владелец получает `manage` сразу после регистрации.

### Relationships и reconciliation

Change [`add-restaurant-delivery-relationships`](../../openspec/changes/add-restaurant-delivery-relationships/) (FR-443, FR-445). Источник истины — `Restaurant` в хранилище `restaurants`; SpiceDB — производный граф.

| Relation | Источник истины | Запись |
| --- | --- | --- |
| `restaurant:<id>#owner@user:<ownerId>` | `Restaurant.ownerId` | Register Restaurant: `TOUCH` до сохранения |
| `restaurant:<id>#staff@user:<userId>` | `Restaurant.staff` (множество user id) | Assign Staff: `TOUCH`, Revoke Staff: `DELETE` — до сохранения |

Writer — `SpiceDbRelationshipWriter` (`infrastructure/spicedb/relationships.ts`), реализует порты `OwnerRelationshipWriter` и `StaffRelationshipWriter` слайса `restaurant`. Запись синхронная, с deadline `SPICEDB__TIMEOUT`; ошибка или timeout — `AuthorizationUnavailableError`, команда отменяется без сохранения (fail closed). Assign/Revoke Staff требуют `restaurant#manage`; повтор — no-op без записи. `cmd/restaurants` собирает Register Restaurant, Assign Staff и Revoke Staff с `SpiceDbAccessControl` и этим writer над одним SpiceDB-клиентом и той же MongoDB-коллекцией `restaurants`, что читает reconciler.

`RelationshipReconciler` (`slices/restaurant/infrastructure/reconcile`) повторяет алгоритм `orders`: graph → business — постраничный `ReadRelationships` (`fully_consistent`) по `restaurant#owner` и `restaurant#staff` с проверкой по хранилищу; business → graph — по всем ресторанам. Orphan или stale связь удаляется только если она была сиротой на двух проходах с интервалом не меньше grace. Missing связь восстанавливается `TOUCH` по тому же правилу — только если отсутствовала с тем же пользователем на двух проходах с интервалом не меньше grace (FR-445, AC-700): Revoke Staff делает `DELETE` до сохранения ресторана, а MongoDB не держит lock, которого reconciler мог бы дождаться (как `FOR SHARE` в `orders`), поэтому проход, прочитавший граф после `DELETE` и ресторан до сохранения, иначе вернул бы снятому сотруднику доступ к кухне и заказам до истечения orphan grace. Missing-кандидат дополнительно привязан к версии документа `Restaurant` (AC-710): если между проходами пользователя снова назначили и снова сняли (новый revoke ещё не сохранён), версия другая и grace начинается заново, а не восстанавливает только что удалённую связь. Любое другое сохранение ресторана тоже начинает grace заново (лишь откладывает исправление); собственная запись revision после восстановления другой relation переносит кандидатов этого ресторана на новую версию. Кандидат, ставший present или исчезнувший из ресторана (revoke сохранён), забывается без записи; кандидаты обоих видов хранятся в памяти процесса, restart лишь откладывает исправление на grace. id, не подходящие под SpiceDB object id `^[a-zA-Z0-9/_|\-=+]+$`, пропускаются и считаются `unrepairable`. Каждый проход пишет в log `checked`, `orphans_pending`, `orphans_deleted`, `missing_pending`, `missing_repaired`, `unrepairable`.

Reconciler — фоновый цикл (`PeriodicLoop`, `pkg/lifecycle/periodic.ts`) внутри основного процесса: первый проход через `min(10s, RECONCILER__INTERVAL)` после старта, следующие — через интервал после окончания предыдущего; ошибка прохода пишется в log, цикл продолжается. При shutdown цикл останавливается (текущий проход прерывается между вызовами SpiceDB) до drain gRPC.

| Переменная | Значение | По умолчанию |
| --- | --- | --- |
| `RECONCILER__INTERVAL` | Пауза между проходами (`30s`, `5m`) | `5m` |
| `RECONCILER__GRACE` | Минимальное время, которое связь остаётся сиротой до удаления или missing до восстановления; должно превышать длительность Revoke Staff (`SPICEDB__TIMEOUT` + сохранение) | `10m` |
| `RECONCILER__PAGE_SIZE` | Размер страницы `ReadRelationships` и хранилища, 1…1000 | `500` |

Unit-тесты: `test/staff-applications.test.ts`, `test/spicedb-relationships.test.ts`, `test/reconcile.test.ts` (fake клиент), `test/periodic.test.ts`. Integration-тесты `test/spicedb-relationships-integration.test.ts` (AC-476, AC-478) используют уникальный префикс id и ограничивают reconciler им, чтобы не трогать чужие связи общего SpiceDB; `test/reconcile.test.ts` (AC-710) — повторное назначение и снятие между проходами; `test/reconcile-mongodb-integration.test.ts` (AC-700) воспроизводит Revoke Staff в полёте над MongoDB replica set и SpiceDB ; все они идут на SpiceDB testcontainer global setup (`npm test`).

TypeScript-код генерируется в `infrastructure/grpc/gen` командой `npm run generate:proto` (`buf generate` по `infrastructure/grpc/buf.gen.yaml`, плагин `buf.build/bufbuild/es`).

## Минимальная доменная модель

Для учебной реализации достаточно трёх небольших агрегатов.

| Тип                                               | Минимальные поля                             | Назначение                                             |
| ------------------------------------------------- | -------------------------------------------- | ------------------------------------------------------ |
| `Restaurant` — aggregate root (slice `restaurant`) | `id`, `name`, `ownerId`, `isOpen`, `address`, `staff` | Управляет данными, pickup-адресом, открытостью ресторана и множеством персонала |
| `Menu` — aggregate root (slice `menu`)            | `id`, `restaurantId`, `items`                | Одно меню на ресторан; управляет позициями меню        |
| `MenuItem` — entity внутри `Menu`                 | `id`, `name`, `description`, `price`, `isAvailable` | Описывает блюдо, его текущую цену и доступность  |
| `Money` — value object `Menu`                     | `amount` (decimal-строка), `currency`        | Неотрицательная цена `MenuItem` без потери точности    |
| `RestaurantOrder` — aggregate root                      | `id`, `orderId`, `restaurantId`, `items`, `state`, timeline | Управляет принятием и приготовлением заказа рестораном |
| `PreparationItem` — entity внутри `RestaurantOrder`     | `menuItemId`, `quantity`, `options`          | Фиксирует состав принятого на приготовление заказа; `options` — непрозрачные id выбранных опций, модель modifiers не согласована |

`Kitchen Order` — роль `RestaurantOrder` в процессе кухни, а не отдельный aggregate; прежнее имя `KitchenOrder` — legacy. Timeline: `received_at`, `accepted_at`, `queued_at`, `preparation_started_at`, `expected_ready_at`, `ready_at`.

Концептуальные состояния `RestaurantOrder`: `RECEIVED`, `ACCEPTED`, `QUEUED`, `PREPARING`, `ASSEMBLED`, `READY`; terminal negative — `REJECTED`, `CANCELLED`, `FAILED`. Transition table — в [FSM: Kitchen / Preparation](../../docs/fsm.md#kitchen--preparation).

Инварианты:
- меню содержит уникальные `MenuItem.id`;
- `orderId` уникален: один `RestaurantOrder` на один `Order`;
- `menuItemId` с одинаковыми options не повторяется внутри одного `RestaurantOrder`;
- цена блюда неотрицательна;
- принять можно только полученный заказ, когда ресторан открыт и все позиции доступны;
- отклонить можно только полученный заказ; отказ после Acceptance — отдельное продуктовое решение;
- приготовление начинается только для заказа в Kitchen Queue;
- заказ не становится `READY`, пока не готовы все required items: готовность допустима только после явного Mark Assembled и не наступает автоматически от последней позиции;
- после terminal state (`READY`, `REJECTED`, `CANCELLED`, `FAILED`) состояние больше не изменяется.

Проверка открытости ресторана и доступности меню выполняется доменной моделью при принятии `RestaurantOrder`; application handler только загружает необходимые агрегаты и координирует вызов.

### Value objects

| VO         | Значение и инварианты                                                                                                                                                  |
| ---------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Money`    | Точная десятичная цена на основе [`decimal.js`](https://github.com/MikeMcl/decimal.js) и код валюты; цена неотрицательна, арифметика разрешена только для одной валюты |
| `Quantity` | Положительное целое число                                                                                                                                              |

`Money` создаётся из строки или другого точного представления, но не из JavaScript `number`. Требуемые масштаб и режим округления явно задаются доменной операцией.

Идентификаторы и статусы пока остаются типизированными значениями и перечислениями, а не отдельными value objects.

### Domain policies и Specification Pattern

Переиспользуемые условия вне state остаются в domain model `restaurants`. Реализованы:

- `RestaurantAcceptingOrders` — часы работы, расписание исключений и текущая capacity;
- `MenuItemAvailable` — окно доступности, остаток/capacity и временные ограничения предложения.

Допустимость переходов по State (`RECEIVED → ACCEPTED/REJECTED`, `QUEUED → PREPARING`, `ASSEMBLED → READY` и другие) определяет только FSM `RestaurantOrder`. Кандидат на новую spec — `RestaurantOrderCancellable` для отмены во время Preparation. Application загружает `Restaurant` и `RestaurantOrder`, получает `now` и capacity snapshot, затем вызывает domain behavior/domain service; authorization не входит в spec. Общие правила — в [Specification Pattern](../../docs/specification-pattern.md) и [ADR 22](../../docs/adr/0022-specification-pattern-for-domain-policies.md).

## Хранилище: MongoDB

MongoDB предлагается как основное логическое хранилище `restaurants`. Документная модель соответствует данным Restaurant, меню и категорий, Menu Item, вариантов блюд, модификаторов и дополнительных опций. Это самостоятельное хранилище контекста, а не read store или проекция `orders`.

Граница документа определяется доменной моделью и согласованными сценариями чтения и изменения. Данные, которые обычно используются и изменяются совместно, можно хранить вложенными. Самостоятельные или растущие наборы данных можно хранить отдельно со ссылками. Один Restaurant со всеми меню и всей историей не становится автоматически одним документом: размер, частота изменений и паттерны доступа учитываются явно.

Для каждого вида документа определяются структура, validation rules и необходимые indexes. Изменение формата документов требует управляемой эволюции схемы. Если операция затрагивает несколько документов, требования к атомарности формулируются в сценарии до выбора механизма реализации; document model не должна скрывать междокументную согласованность.

Направление для кухни: aggregate `RestaurantOrder` — отдельный document; Kitchen Queue и read models kitchen board — projections; один документ на ресторан вместе с меню и заказами не используется ([Kitchen](../../docs/kitchen.md#temporal-и-persistence)). Конкретные collections, поля, document boundaries и indexes проектируются по согласованным SDD-сценариям. Реализации репозиториев, BSON-модели и преобразования находятся в `infrastructure`; доменная модель и прикладные контракты не зависят от BSON, MongoDB driver или ODM.

Решение имеет статус Proposed в [ADR 8](../../docs/adr/0008-storage-strategy-postgresql-and-mongodb.md); коллекции, границы документов, validators, индексы, эволюция схемы и атомарность конкретизированы в локальном [ADR 2](docs/adr/0002-mongodb-collections-and-document-boundaries.md).

Реализация: официальный драйвер `mongodb` без ODM. Общие части — `pkg/infrastructure/mongodb` (`client.ts`, `unit-of-work.ts` — UoW как multi-document транзакция, `outbox.ts` — коллекция `outbox` по [ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md) в контракте CDC [ADR 37](../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md) с внедряемым mapper Domain Event → Protobuf (по умолчанию ничего не пишет; `cmd/restaurants` подключает mapper процесса, который по `aggregate_type` выбирает mappers слайсов `restaurant`, `menu` и `restaurantorder`), `versioned-collection.ts` — optimistic concurrency по `version`, `migrate.ts` — запуск [migrate-mongo](https://github.com/seppevs/migrate-mongo) и шаг `ensureCollection`), адаптеры слайсов — `slices/<slice>/infrastructure/mongodb`, миграции — `infrastructure/mongodb/migrations/`. Коллекции: `restaurants`, `menus`, `restaurant_orders`, `simulator`, `outbox`, `inbox`, `migrations_changelog`, `migrations_lock`. Транзакции требуют replica set.

| Переменная | Значение | По умолчанию |
| --- | --- | --- |
| `MONGODB__URI` | connection string replica set; обязателен — без него `restaurants` и `restaurants-consumer` не стартуют | — |
| `MONGODB__DATABASE` | имя базы | `restaurants` |
| `MONGODB__TIMEOUT` | server selection timeout; недоступная MongoDB останавливает старт | `5s` |
| `MONGODB__MAX_POOL_SIZE` | соединений в пуле одного сервера replica set (1..100), `maxPoolSize` | `restaurants` 20, `restaurants-consumer` 5 |
| `MONGODB__MIN_POOL_SIZE` | прогретые соединения (0..`MAX_POOL_SIZE`), `minPoolSize` | `0` |
| `MONGODB__MAX_IDLE_TIME` | idle-соединение закрывается после этого срока, `maxIdleTimeMS` | `5m` |
| `MONGODB__WAIT_QUEUE_TIMEOUT` | ожидание свободного соединения до ошибки checkout, `waitQueueTimeoutMS` | `5s` |
| `MONGODB__MAX_CONNECTING` | одновременно устанавливаемые соединения (1..`MAX_POOL_SIZE`), `maxConnecting` | `2` (не больше `MAX_POOL_SIZE`) |

Connection pool — один `MongoClient` на процесс (`pkg/infrastructure/mongodb/client.ts`); драйвер держит пул на каждый сервер replica set, лимиты действуют на пул одного сервера и перекрывают одноимённые параметры URI. Default `MAX_POOL_SIZE` выбирает процесс (`loadConfig(env, { process })`); значение вне диапазона или нулевой таймаут (у драйвера 0 означает «без ограничения») останавливает старт. Метрики пула — CMAP events драйвера (`pool-metrics.ts`) по semconv `db.client.connection.*` (`count` по `state` idle | used, `max`, `idle.min`, `pending_requests`, `timeouts`, `wait_time`, `use_time`, `create_time`) с `db.client.connection.pool.name` = `<host:port>/<database>`, плюс `nomnom.db.client.connection.pool.clears` ([инвентарь](../../docs/observability/metric-inventory.md#db)); панели и alert rules пула — row Dependencies overview ([ops/grafana](ops/grafana/README.md)).

Миграции встроены в приложение и выполняются библиотекой migrate-mongo ([ADR 39](../../docs/adr/0039-data-access-without-orm.md)): `restaurants` и `restaurants-consumer` при старте, до обслуживания, вызывают `migrateRestaurantsDatabase` своим `MongoClient` — применяются по порядку ещё не записанные в `migrations_changelog` файлы `infrastructure/mongodb/migrations/<NNNN>-<владелец>-<изменение>.ts` (владелец — слайс или `pkg`, чью коллекцию меняет файл), либо процесс пишет `nothing to migrate` и продолжает. Ошибка миграции останавливает процесс. Одновременный старт процессов сериализует lock migrate-mongo в `migrations_lock` (атомарный за счёт unique index, занятый lock ждётся, lock упавшего процесса удаляет TTL); миграции идемпотентны и применимы к базе прежнего мигратора ([локальный ADR 2](docs/adr/0002-mongodb-collections-and-document-boundaries.md#эволюция-схемы)). Изменение validator или индексов — новый файл миграции, применённые файлы не редактируются; отдельного шага migrate нет.

## Событийная интеграция

Consumers интеграционных событий `restaurants` используют Transactional Inbox по общесистемному [ADR 11](../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md). Проверка `event_id`, изменение документов контекста, запись Inbox и, если обработчик публикует новое событие, запись Outbox выполняются в одной MongoDB-транзакции. Уникальность `{ consumer, event_id }` обеспечивается unique index; конфликт индекса откатывает транзакцию и считается duplicate. Kafka offset подтверждается только после commit.

### Публикация: Outbox и Debezium

Публикация Integration Events реализована по [ADR 13](../../docs/adr/0013-transactional-outbox-for-integration-event-publishing.md), [ADR 31](../../docs/adr/0031-outbox-storage-polling-relay-and-clients.md) (хранилище) и [ADR 37](../../docs/adr/0037-outbox-delivery-via-debezium-cdc.md) (доставка CDC):

- **Mappers** — явные allowlist Domain Event → Protobuf `nomnom/restaurants/v1/events.proto`, по одному стриму на aggregate, чтобы порядок по `aggregate_id` (Kafka key) сохранялся внутри стрима:

  | Topic | `aggregate_type` / key | Integration Events | Mapper |
  | --- | --- | --- | --- |
  | `restaurants.v1.restaurant-order-events` | `RestaurantOrder` / id Kitchen Order | `RestaurantOrderAccepted` (`expected_ready_at`), `RestaurantOrderRejected` (`reason` пустой, если не указан), `PreparationStarted`, `PreparationDelayed`, `RestaurantOrderFailed` → `PreparationFailed`, `RestaurantOrderReady`, `RestaurantOrderCancelled` (`reason`, `preparation_started`) | `slices/restaurantorder/infrastructure/integration/restaurant-order-events.ts` |
  | `restaurants.v1.restaurant-events` | `Restaurant` / id ресторана | `RestaurantOpened`, `RestaurantClosed` (`restaurant_id`) | `slices/restaurant/infrastructure/integration/restaurant-events.ts` |
  | `restaurants.v1.menu-events` | `Menu` / id меню | `MenuItemAvailabilityChanged` (`menu_id`, `restaurant_id`, `menu_item_id`, `available`) — от Set Menu Item Availability и от Update Menu Item при смене доступности | `slices/menu/infrastructure/integration/menu-events.ts` |

  Кроме Integration Events, каждая запись Restaurant Order и Restaurant кладёт в тот же Outbox Kitchen Change Notification — JSON `{"__typename":"Kitchen","restaurantId":…}` в `restaurants.v1.kitchen-changes` (`slices/restaurantorder/infrastructure/integration/kitchen-changes.ts`). Его читает только Cosmo Router BFF для subscription `kitchenChanged` ([ADR 36](../../docs/adr/0036-graphql-subscriptions-via-cosmo-streams.md)). Notifier открывает Unit of Work вокруг записи или присоединяется к активному.

  Внутренними остаются `RestaurantOrderReceived`, `Queued`, `Assembled`, `RestaurantRegistered`, `RestaurantRenamed`, `RestaurantAddressChanged`, `MenuCreated`, `MenuItemAdded`, `MenuItemUpdated`; staff — relationships SpiceDB без Domain Events. Каждый слайс регистрирует свой mapper в Awilix, `di/restaurants.ts` компонует их `outboxEventMapperByAggregate` (`pkg/infrastructure/outbox/integration-event.ts`) и передаёт `MongoOutbox`; `traceparent` берётся из активного span (server span gRPC-вызова или consumer span сообщения, см. [Observability](#observability)). `event_id` уникален для `aggregate_type:aggregate_id:aggregate_version:event_type`, поэтому одна версия меню может нести не более одного `MenuItemAvailabilityChanged`. Change [`publish-restaurant-and-menu-events`](../../openspec/changes/publish-restaurant-and-menu-events/).
- **`event_id`** — UUIDv5 (namespace `8f1d3c2a-5b7e-4a90-9c61-2e4f7b8d0a13`) от `"{aggregate_type}:{aggregate_id}:{aggregate_version}:{event_type}"`, `event_type` — полное имя Protobuf-сообщения.
- **Доставка** — коннектор Debezium `restaurants-outbox` (MongoDB, change streams, `MongoEventRouter`) в Kafka Connect читает вставки в `restaurants.outbox` сразу после commit и публикует их в топик из поля `topic` с key `aggregateId` и value `payload` (байты как есть). Своего процесса публикации у контекста нет: сервис только пишет запись в транзакции изменения. Конфигурацию коннектора держит chart (`cdc.*`, см. [ops/README.md](ops/README.md#cdc-коннектор-outbox)).
- **Запись Outbox** всегда содержит все поля контракта ADR 37: `eventId`, `eventType`, `aggregateType`, `aggregateId`, `aggregateVersion` (Long), `occurredAt` (Date), `topic`, `payload` (Binary), `traceparent` (`""` без trace context) и `createdAt`. Документ без поля заголовка останавливает коннектор. TTL-индекс `outbox_created_at_ttl` удаляет записи через час после `createdAt`; `update` и `delete` коннектор пропускает. Миграция `0001-outbox-cdc-drop-relay` удаляет индексы relay, коллекции `outbox_counters` и `outbox_leases` и записи без `createdAt`.
- **Kafka headers** совпадают с `orders` (`pkg/messaging/envelope.go`): `event_id`, `event_type`, `occurred_at` (RFC 3339 UTC, миллисекунды), `aggregate_type`, `aggregate_id`, `aggregate_version`, `traceparent` (пустой без trace context), а также `_watermill_message_uuid` = `event_id`.
- **Метрики** доставки — метрики Kafka Connect и Debezium (JMX), своих `nomnom.outbox.*` у контекста нет.

### Потребление: Inbox и restaurants-consumer

`cmd/restaurants-consumer` (`npm run consumer`, в образе — `node dist/cmd/restaurants-consumer/main.js`) читает стрим `orders.v1.order-events` consumer group `KAFKA__CONSUMER_GROUP` (по умолчанию `restaurants`) клиентом `@confluentinc/kafka-javascript` (ADR 31) и следует runtime contract `pkg/lifecycle` (только технический порт): по `SIGTERM` начатое сообщение дообрабатывается до `SHUTDOWN__DRAIN_TIMEOUT`, затем `disconnect` коммитит offsets. Пустой `MONGODB__URI` или `KAFKA__BROKERS` — ошибка startup.

- **Событие:** `nomnom.orders.v1.OrderPlaced` (`orders/infrastructure/grpc/nomnom/orders/v1/events.proto`, TypeScript-код генерируется из workspace в `infrastructure/grpc/gen/nomnom/orders`), envelope — Kafka headers `event_id`, `event_type`, `occurred_at`, `aggregate_*`. Handler `slices/restaurantorder/infrastructure/integration/order-placed.ts` переводит payload в Command **Receive Order Request** (`restaurant_id`, `order_id`, `items[].menu_item_id`, `items[].quantity`) — тот же handler, что у RPC `ReceiveOrderRequest`, поэтому оба пути сходятся к одному `RECEIVED` Restaurant Order. Subject — `service:orders` (источник стрима); access control процесса (`OrdersEventSourceAccessControl`) разрешает ему только `canCoordinateFulfillment`, без SpiceDB: подлинность события обеспечивает владение topic. Остальные типы стрима (`OrderCompleted`, `OrderCancelled`) подтверждаются без обработки.
- **Inbox** (`pkg/infrastructure/mongodb/inbox.ts`, коллекция `inbox`): проверка `(consumer, event_id)`, запись Inbox и сохранение Restaurant Order — одна транзакция `MongoUnitOfWork`; unique index `{consumer, eventId}`, повтор — `duplicate` без бизнес-действия; конкурентная доставка даёт WriteConflict, driver повторяет транзакцию. Retention — TTL index `processedAt` 30 дней. Consumer — `restaurants.order-placed`.
- **Ошибки** (`pkg/infrastructure/messaging/processor.ts`): битый envelope или payload, `OrderPlaced` без `items`, невалидное количество — `orders.v1.order-events.dlq` с metadata `reason_poisoned`, `topic_poisoned`, `consumer_group_poisoned` (как PoisonQueue в `orders`) и ack; неизвестный ресторан или конфликтующий состав уже полученного заказа — бизнес-исход, ack и log; прочее (MongoDB) — transient: `CONSUMER__MAX_RETRIES` повторов с backoff `CONSUMER__RETRY_INITIAL_INTERVAL`…`CONSUMER__RETRY_MAX_INTERVAL`, затем Kafka доставляет сообщение снова.
- **Метрики:** `nomnom_messaging_process_duration_seconds`, `nomnom_messaging_retry_attempts_total`, `nomnom_messaging_poison_messages_total`, `nomnom_messaging_message_age_seconds`, `nomnom_inbox_duplicates_total` (labels `handler_name`, `event_type`, `stream`, `error_class`).

| Переменная | Назначение | По умолчанию |
| --- | --- | --- |
| `KAFKA__BROKERS` | обязателен для `restaurants-consumer` | — |
| `KAFKA__CLIENT_ID` | client id consumer и producer dead letters | `restaurants-consumer` |
| `KAFKA__CONSUMER_GROUP` | consumer group | `restaurants` |
| `KAFKA__INITIAL_OFFSET` | начало чтения новой группы: `oldest` или `newest` | `oldest` |
| `CONSUMER__MAX_RETRIES` | повторы transient-ошибки в процессе, 0…100 | `5` |
| `CONSUMER__RETRY_INITIAL_INTERVAL`, `CONSUMER__RETRY_MAX_INTERVAL` | экспоненциальный backoff повторов | `100ms`, `5s` |

Change [`add-restaurants-order-placed-consumer`](../../openspec/changes/add-restaurants-order-placed-consumer/). Общие правила — [ADR 11](../../docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md), [документация событийного взаимодействия](../../docs/event-driven-architecture.md) и [Unit of Work](../../docs/unit-of-work.md#restaurants--mongodb).

## Dependency Injection

В `restaurants` используется Awilix. Регистрация зависимостей находится в composition root каждого исполняемого процесса. Handlers и прикладные компоненты получают зависимости явно через параметры конструкторов или фабрик; прикладной код не вызывает `container.resolve()`, а доменная модель не зависит от Awilix.

Время жизни определяется назначением зависимости: общие клиенты получают application scope, а ресурсы конкретной обработки — request или message scope. В тестах handlers и прикладные компоненты создаются с явно переданными подменами.

Структура:

- `slices/<slice>/di/registrations.ts` — регистрации слайса: MongoDB adapters его ports, MVP adapters и handlers. Модуль объявляет `<Slice>Dependencies` (что берёт у процесса) и `<Slice>Cradle` (что предоставляет); тип `Registrations<Cradle>` из `pkg/di/container.ts` проверяет при компиляции, что зарегистрирован каждый ключ с нужным типом, а сигнатура `register<Slice>` — что процесс даёт все зависимости.
- `di/restaurants.ts`, `di/consumer.ts` — контейнеры процессов: общие клиенты (MongoDB Unit of Work, Outbox, SpiceDB client, access control, relationship writer), модули слайсов, gRPC server и фоновые циклы. Фабрики регистраций — единственное место, где разбирается cradle; handlers получают обычный объект зависимостей.
- `cmd/*/main.ts` — точки входа: читают конфигурацию, проверяют обязательную инфраструктуру, асинхронно открывают ресурсы (MongoDB, Kafka producer, subject resolver, telemetry), передают их контейнеру как values, мигрируют MongoDB, запускают `Lifecycle` с `cradle.worker` и освобождают ресурсы в фиксированном порядке: `container.dispose()` (disposer закрывает SpiceDB client), затем Kafka producer, MongoDB и telemetry.

Контейнер создаётся в PROXY injection mode со `strict: true`: регистрация с большим временем жизни не может зависеть от более короткоживущей. Сейчас все регистрации — application scope (singleton): handlers без состояния поверх общих клиентов, а ресурс одной операции — MongoDB session транзакции — принадлежит `MongoUnitOfWork.run` и живёт ровно один use case. В `restaurants-consumer` ресурс одного сообщения — MongoDB session транзакции Inbox — так же принадлежит `MongoUnitOfWork.run` (async context), поэтому отдельный message scope (`container.createScope()`) не нужен. `test/composition.test.ts` проверяет, что каждый контейнер разрешает все регистрации.

Решение зафиксировано в общесистемном [ADR 5](../../docs/adr/0005-dependency-injection-strategy.md).

## Observability

Контракт — [docs/observability.md](../../docs/observability.md), lifecycle — [cloud-native runtime](../../docs/cloud-native-runtime.md#observability-lifecycle). Bootstrap — `pkg/telemetry`; инструментирование транспорта, MongoDB, Kafka и внешних клиентов — в `infrastructure/` и `pkg/infrastructure/`; logger и use case decorator собираются в composition root (`cmd/*`, `di/`). Одинаково для `restaurants`, `restaurants-consumer` и `restaurants-relay`.

**Resource** (`pkg/telemetry/resource.ts`) один для traces, metrics и logs: `service.name` (по умолчанию имя процесса; `OTEL_SERVICE_NAME` > `OTEL_RESOURCE_ATTRIBUTES` > default), `service.namespace=nomnom` всегда, `service.version`, `service.instance.id`, `deployment.environment.name` из `OTEL_RESOURCE_ATTRIBUTES`, плюс `telemetry.sdk.*` и `process.runtime.*`.

**Traces** (`pkg/telemetry/tracing.ts`): `startTelemetry` всегда регистрирует AsyncLocalStorage context manager и W3C Trace Context; Baggage — только ключи `BAGGAGE_ALLOWLIST` (сейчас пусто), некорректный `traceparent` начинает новый trace. `TracerProvider` с OTLP/gRPC exporter и `BatchSpanProcessor` с ограниченной очередью (`SPAN_EXPORT_LIMITS`: 2048 spans, batch 512) создаётся только при `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` или `OTEL_EXPORTER_OTLP_ENDPOINT`. Spans (semconv v1.44):

- SERVER `nomnom.restaurants.v1.RestaurantsService/<Method>` (`infrastructure/grpc/tracing.ts`): `rpc.system.name`, `rpc.method`, `rpc.response.status_code` (имя кода); статус Error только для серверных кодов (`UNKNOWN`, `DEADLINE_EXCEEDED`, `UNIMPLEMENTED`, `INTERNAL`, `UNAVAILABLE`, `DATA_LOSS`);
- `usecase <name>` — декоратор `UseCaseTelemetry` (`pkg/telemetry/usecase.ts`), подключаемый `di/usecases.ts` ко всем handlers без изменения слайсов;
- CLIENT gRPC SpiceDB (`infrastructure/grpc/client-telemetry.ts`, interceptor `@grpc/grpc-js`): `traceparent` в metadata вызова;
- CLIENT MongoDB `<command> <collection>` из command monitoring драйвера (`pkg/infrastructure/mongodb/command-telemetry.ts`, `monitorCommands: true`): только `db.operation.name`, `db.collection.name`, `db.namespace`, `server.*`; команды, фильтры и документы не читаются, handshake-команды пропускаются;
- Outbox хранит `traceparent` активного span при записи события, Debezium переносит его в Kafka header как есть: consumer продолжает trace вызова без PRODUCER span;
- PRODUCER `send <topic>` (`KafkaPublisher`, `sendWithTraceContext`): dead letters consumer, `traceparent`/`tracestate` producer span в Kafka headers;
- CONSUMER `process <topic>` (`KafkaConsumerWorker`): дочерний span header `traceparent` (одно сообщение — одна обработка), `messaging.consumer.group.name`, `messaging.message.id`; повторная доставка — отдельный span.

Ошибка в span — только `error.type` (stable `code` или имя класса) и статус; текст ошибки и exception events не пишутся.

**Metrics** (OTLP gRPC при `OTEL_EXPORTER_OTLP_ENDPOINT` или `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`, интервал 15 s): `rpc.server.call.duration` (`rpc.system.name`, `rpc.method`, `rpc.response.status_code`), `rpc.client.call.duration` (+ `server.address`, `server.port`, `error.type` при неуспехе) — `pkg/telemetry/rpc.ts`, buckets в секундах; `nomnom.usecase.duration` (`nomnom.usecase.name`, `nomnom.usecase.kind`, `nomnom.outcome` ∈ `success|rejected|error`, `error.type` при неуспехе; классификация — `classifyByCode`); `db.client.operation.duration` (`db.system.name`, `db.operation.name`, `db.collection.name`, `error.type`); runtime — `@opentelemetry/instrumentation-runtime-node` (`v8js.memory.heap.used`, `v8js.memory.heap.space.*`, `v8js.gc.duration`, `nodejs.eventloop.delay.*`, `nodejs.eventloop.utilization`) и `pkg/telemetry/process-metrics.ts` (`process.cpu.time`, `process.memory.usage`, `v8js.memory.heap.limit`); пул MongoDB, Outbox, consumer и domain-метрики — см. разделы выше.

**Logs** (`pkg/telemetry/logger.ts`): одна JSON-запись на строку в stdout — `timestamp` (UTC), `severity` (`DEBUG|INFO|WARN|ERROR`), `message`, `service.namespace`, `service.name`, `service.version`, `service.instance.id`, `deployment.environment.name`, `trace_id`/`span_id` только внутри span, `operation` (`lifecycle`, `identity`, `reconcile`, `kafka.consume`, `migrate`, `process`) и поля ошибки `error.type`, `error.message`, `error.stack` (массив frames), `error.cause`. Значения ключей вида token/password/secret/authorization/cookie/credential/preshared/assertion заменяются на `[REDACTED]`, пароли в URL и bearer/basic tokens вырезаются из текста. Необработанная ошибка (в том числе при старте) — одна запись ERROR и exit 1. Библиотечный код по умолчанию не пишет логов (тесты тихие).

**Shutdown**: telemetry стартует до приложения и завершается последней — `telemetry.shutdown(SHUTDOWN__TELEMETRY_FLUSH_TIMEOUT)` (по умолчанию `5s`) выполняет финальный export traces и metrics и не ждёт недоступный collector дольше deadline.

Тесты — `test/observability.test.ts` (in-memory span exporter и metric reader: trace через gRPC → use case → Outbox → Kafka headers → consumer, grpc-js client, MongoDB, runtime-метрики, JSON-логи, baggage, bounded flush) и `test/tracing.test.ts`. Profiles (`@pyroscope/nodejs`) пока не подключены.

## Статус реализации

Реализованы доменная модель и application-слой по change [`implement-restaurants-application-layer`](../../openspec/changes/implement-restaurants-application-layer/) (требования `FR-200`…`FR-243`, acceptance criteria `AC-200`…`AC-283`; решения и допущения — в [design.md](../../openspec/changes/implement-restaurants-application-layer/design.md)).

- **Domain:** aggregate `Restaurant`; aggregate `Menu` с entity `MenuItem` и VO `Money` на `decimal.js` (меню — отдельный aggregate slice `menu`, одно на ресторан); aggregate `RestaurantOrder` с чистой FSM в виде transition table, VO `Quantity`, domain service `evaluateOrderAcceptance` поверх `RestaurantAcceptingOrders` и `MenuItemAvailable`, Domain Events с `pullEvents()`; исход Cancel (`cancelled`, `repeated`, `rejected` с причиной по стадии) возвращает `RestaurantOrder.cancel()` как `CancellationDecision`; контракты repositories в `domains/`.
- **Application:** по одному handler на use case в `slices/<slice>/applications/<usecase>/`: Register/Update/Open/Close Restaurant, Assign/Revoke Staff, Get/List Restaurants, List Owned Restaurants; Create Menu, Add/Update Menu Item, Set Menu Item Availability, Get Menu; Receive Order Request, Accept Order (с Queue в одной UoW), Reject Order, Queue Restaurant Order, Start Preparation, Mark Assembled, Mark Ready for Pickup, Report Preparation Delay/Failure, Cancel Restaurant Order, Get Restaurant Order, List Incoming Orders, Get Kitchen Queue, List Active Preparations, List Ready Orders.
- **Порты:** `UnitOfWork`, `Outbox`, `Clock`, `IdGenerator`, `Subject`, application errors и общие шаги `requirePermission` (решение порта доступа → `PermissionDeniedError`) и `publishAggregateChange` (save → отбор опубликованных фактов → Outbox) в `pkg/application`; узкие authorization ports в `applications/access-control.ts` каждого слайса (в `restaurantorder` — по одному на вид вызывающего: персонал кухни, fulfillment `orders`, чтение заказов) и `OwnerRelationshipWriter`; `restaurantorder` читает чужие слайсы только через `RestaurantExistence` (Receive Order Request) и `AcceptanceFactsReader` (Accept Order: открыт ли ресторан и доступность позиций), а не через их repositories; общие модули слайса `restaurantorder` — `restaurant-order-view.ts`, `load-restaurant-order.ts`, `publish-order-change.ts`, `kitchen-command.ts` (шаблон команды кухни и `RepeatPolicy`); query/read-model ports рядом с queries; ports открытых решений — `AcceptanceConditionsProvider`, `ReadyTimeEstimator`, `KitchenQueueOrdering`. Политика повтора Start Preparation/Mark Ready и политика отмены во время Preparation — обязательные параметры handlers без значения по умолчанию; значения задаёт конфигурация (`KITCHEN__*`, см. [gRPC-адаптер](#grpc-адаптер)).
- **Адаптеры:** MongoDB (`pkg/infrastructure/mongodb`, `slices/*/infrastructure/mongodb`, см. [Хранилище: MongoDB](#хранилище-mongodb)) — UoW как транзакция, Outbox-коллекция, optimistic concurrency по `version`, уникальность `orderId` и меню на ресторан индексами; in-memory адаптеров хранилища нет — тесты application-слоя и transport идут через MongoDB-адаптеры на testcontainer (см. [Проверка](#проверка)); SpiceDB adapter authorization ports подключается при заданном `SPICEDB__ENDPOINT` (см. [Authorization: SpiceDB](#authorization-spicedb)); writer relationships `restaurant#owner`/`staff` и фоновый reconciler (см. [Relationships и reconciliation](#relationships-и-reconciliation)); mappers Integration Events Kitchen Order, Restaurant и Menu с доставкой в Kafka через Debezium (см. [Публикация](#публикация-outbox-и-debezium)); consumer `OrderPlaced` с Inbox — `cmd/restaurants-consumer` (см. [Потребление](#потребление-inbox-и-restaurants-consumer)). Через transport в Outbox пишут `CancelRestaurantOrder`, команды кухни, Open/Close Restaurant и команды меню. `GetRestaurant`, `GetMenu`, `ListRestaurants`, `ReceiveOrderRequest`, `CancelRestaurantOrder`, команды кухни (`AcceptRestaurantOrder`, `RejectRestaurantOrder`, `StartPreparation`, `MarkRestaurantOrderAssembled`, `MarkRestaurantOrderReady`, `ReportPreparationDelay`, `ReportPreparationFailure`, change [`add-kitchen-command-rpcs`](../../openspec/changes/add-kitchen-command-rpcs/)), `RegisterRestaurant`, `AssignStaff`, `RevokeStaff`, `OpenRestaurant`, `CloseRestaurant` и команды меню (`CreateMenu`, `AddMenuItem`, `UpdateMenuItem`, `SetMenuItemAvailability`, change [`add-restaurant-menu-command-rpcs`](../../openspec/changes/add-restaurant-menu-command-rpcs/)) и queries `ListIncomingOrders`, `GetKitchenQueue` (change [`close-order-lifecycle-in-kind`](../../openspec/changes/close-order-lifecycle-in-kind/)) подключены к `cmd/restaurants` через Connect/gRPC (командные RPC с `ServiceTokenSubjectResolver` при заданной конфигурации identity, иначе с `RejectingSubjectResolver`), к transport пока не подключены Update Restaurant, Get Restaurant Order, List Active Preparations и List Ready Orders.
- **Не реализованы:** Start / Complete Preparation Item (item-level model не входит в MVP) и Reject After Acceptance (не разрешён до продуктового решения); courier projection в List Ready Orders; BDD `.feature`.

## Запуск

Требуется Node.js 22.6 или новее с поддержкой запуска TypeScript через type stripping.

Нужны MongoDB replica set и SpiceDB. Локальный single-node replica set:

```bash
docker run -d --name restaurants-mongo -p 27017:27017 mongo:8 --replSet rs0
docker exec restaurants-mongo mongosh --quiet --eval 'rs.initiate()'
export MONGODB__URI='mongodb://localhost:27017/?replicaSet=rs0&directConnection=true'
npm start  # мигрирует базу при старте
# OrderPlaced → Restaurant Order (отдельный процесс, нужен Kafka):
KAFKA__BROKERS=localhost:9092 npm run consumer
```

## Проверка

```bash
npm test
npm run check
```

`npm test` требует Docker: global setup `test/support/global-containers.ts` поднимает на весь прогон три testcontainer с образами как в kind: MongoDB (`@testcontainers/mongodb`, single-node replica set с транзакциями), Kafka (`apache/kafka`, KRaft, один брокер) и SpiceDB (`authzed/spicedb`, memory datastore со schema и grants из `authorization/bootstrap.yaml`, key `devkey`). Адреса передаются файлам через окружение и читаются `mongoUri()`, `kafkaBrokers()`, `spiceDbEndpoint()`; внешние инстансы подставить нельзя, и ни один тест не пропускается. In-memory адаптеров хранилища нет: unit-тесты use cases и gRPC (`test/support/*.ts`) работают на `Mongo*`-адаптерах, каждый test world создаёт свою мигрированную базу с уникальным именем (`test/support/mongodb.ts`), базы удаляются в конце файла. Доменные тесты MongoDB не трогают. Тесты Kafka и SpiceDB изолируются уникальными топиками, группами и префиксами id.

Тесты типизированы: `npm run typecheck` (`tsconfig.test.json`, весь код включая `test/`) входит в `npm run check`. gRPC-тесты вызывают сервис через `createService` (`test/support/grpc.ts`): запрос собирается из init shape, как у клиента.

Автоформатирование:

```bash
npm run format
```

## Контейнер

Образ собирается из каталога `boundaries/restaurants`. Доступные targets: `deps`, `build`,
`production-deps` и `runtime`; первые две стадии содержат инструменты разработки,
`production-deps` — только production-зависимости, а итоговая стадия `runtime` —
скомпилированный JavaScript и Node.js runtime:

```bash
docker build --target runtime -t nomnom/restaurants:local boundaries/restaurants
docker run --rm -p 8082:8082 -p 9082:9082 \
  -e SERVER__PORT=8082 \
  nomnom/restaurants:local
```

Контейнер запускается пользователем `node`; `tini` пересылает сигналы приложению, а
конфигурация передаётся переменными окружения. `pkg/lifecycle` реализует
[runtime contract](../../docs/cloud-native-runtime.md): `/health/startup`, `/health/live` и
`/health/ready` на техническом порту `MANAGEMENT__PORT` (по умолчанию `9082`), а по
`SIGTERM`/`SIGINT` — not ready, `SHUTDOWN__READINESS_DELAY` (по умолчанию `5s`), остановку
приёма запросов и drain в пределах `SHUTDOWN__DRAIN_TIMEOUT` (по умолчанию `20s`).
Файл `.env` предназначен для локальной разработки и в образ не копируется. Основной процесс
требует MongoDB и SpiceDB, `restaurants-consumer` — MongoDB и Kafka. Outbox локально
никто не публикует: Debezium работает только в kind (ADR 37).

## Развёртывание

Базовый Helm chart и команды его проверки и установки описаны в [ops/README.md](ops/README.md).
Операционные файлы приложения находятся в каталоге `ops/`.

Вызывающий может передать ZedToken своей записи в metadata `authorization-revision`: проверки `platform:nomnom` и ресторанов без сохранённой revision в этом запросе идут `at_least_as_fresh` по нему (например, оператор сразу после выдачи `platform#operator`). Права token не расширяет.
