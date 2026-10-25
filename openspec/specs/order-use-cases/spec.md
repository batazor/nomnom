# order-use-cases Specification

## Purpose
Определяет прикладное поведение Commands и Queries bounded context `orders`: оформление `Order`, отражение бизнес-результатов `restaurants` и `delivery`, Cancellation Request, финализацию итога fulfillment и чтение заказов с соблюдением транзакционной границы, идемпотентности и authorization.

## Requirements

### Requirement: Place Order создаёт Order из проверенного snapshot
**ID:** FR-100

Система SHALL создавать `Order` в состоянии `placed` только из позиций одного Restaurant, цены и доступность которых получены из catalog snapshot до транзакции. Total MUST вычисляться моделью из цен snapshot, а не приниматься от клиента. `Order` и Domain Event `OrderPlaced` для Outbox SHALL фиксироваться одной локальной транзакцией.

#### Scenario: Успешное оформление
**ID:** AC-100

- **WHEN** Customer оформляет заказ из доступных позиций с корректным Delivery Address
- **THEN** система сохраняет `Order` в состоянии `placed` с total по ценам snapshot, записывает `OrderPlaced` в Outbox той же транзакции и возвращает `order_id`

#### Scenario: Позиция недоступна или сумма вне лимитов
**ID:** AC-101

- **WHEN** snapshot отмечает позицию недоступной либо total нарушает минимальную или максимальную сумму
- **THEN** система отклоняет оформление с типизированными причинами, не сохраняет `Order`, не пишет Outbox и не запускает fulfillment

#### Scenario: Некорректный состав или адрес
**ID:** AC-102

- **WHEN** запрос не содержит позиций, содержит неположительное количество, повторяющийся Menu Item или пустой Delivery Address
- **THEN** система возвращает ошибку некорректного заказа до обращения к catalog snapshot и ничего не сохраняет

### Requirement: Повтор Place Order идемпотентен по ключу клиента
**ID:** FR-101

Place Order SHALL принимать idempotency key клиента. Повтор с тем же ключом от того же Customer MUST возвращать прежний `order_id` без второго `Order` и второй записи Outbox и SHALL повторно запрашивать старт fulfillment с той же identity процесса.

#### Scenario: Повтор после потерянного ответа
**ID:** AC-103

- **WHEN** Customer повторяет оформление с тем же idempotency key
- **THEN** система возвращает прежний `order_id`, не создаёт новый `Order` и Outbox record и повторяет идемпотентный запрос старта fulfillment

### Requirement: Связь Customer и Order записывается до commit
**ID:** FR-102

Place Order SHALL записывать creation relationship Customer → `Order` в authorization store до локальной транзакции с заранее сгенерированным `order_id`. Ошибка записи MUST отменять оформление (fail closed).

#### Scenario: Authorization store недоступен
**ID:** AC-104

- **WHEN** запись relationship завершается ошибкой
- **THEN** система не сохраняет `Order`, не пишет Outbox и не запускает fulfillment

### Requirement: Сбой старта fulfillment не отменяет оформленный Order
**ID:** FR-103

Старт Order Fulfillment Workflow SHALL выполняться только после успешного commit. Ошибка старта MUST NOT откатывать или удалять зафиксированный `Order` и SHALL отражаться в результате как незавершённый старт, который восстанавливается повтором с тем же idempotency key.

#### Scenario: Workflow недоступен после commit
**ID:** AC-105

- **WHEN** commit оформления успешен, а запрос старта fulfillment завершается ошибкой
- **THEN** `Order` и `OrderPlaced` остаются зафиксированными, результат содержит `order_id` и признак незапущенного fulfillment

### Requirement: Входящий бизнес-факт применяется не более одного раза
**ID:** FR-104

Каждый Command, отражающий integration event соседнего контекста, SHALL принимать стабильный `event_id` и фиксировать его в Inbox той же транзакцией, что и изменение `Order`. Повтор того же `event_id` MUST NOT изменять `Order`; семантический повтор с новым `event_id` MUST NOT создавать второй бизнес-эффект.

#### Scenario: Повторная доставка того же события
**ID:** AC-106

- **WHEN** событие с уже обработанным `event_id` поступает повторно
- **THEN** `Order` не изменяется, результат отмечается как duplicate, а workflow повторно получает бизнес-факт с тем же `event_id`

#### Scenario: Тот же факт с новым event_id
**ID:** AC-107

- **WHEN** соседний контекст повторно сообщает уже отражённый факт под новым `event_id`
- **THEN** `Order` не изменяется, результат отмечается как уже применённый, а workflow получает факт с новым `event_id` для собственной дедупликации

### Requirement: Confirm Order отражает Order Acceptance
**ID:** FR-105

Confirm Order SHALL переводить `Order` из `placed` в `confirmed` по событию Order Acceptance ресторана и после commit передавать бизнес-факт Order Fulfillment Workflow вместе с исходным `event_id`.

#### Scenario: Ресторан принял заказ
**ID:** AC-108

- **WHEN** `Order` в состоянии `placed` получает событие Order Acceptance
- **THEN** `Order` переходит в `confirmed`, а workflow получает факт принятия с исходным `event_id`

### Requirement: Отказ ресторана сохраняется без финализации Order
**ID:** FR-106

Reject Order SHALL сохранять в `Order` факт отказа ресторана и доступную причину не более одного раза, не меняя Order State. Отрицательный итог MUST фиксироваться только через Fail or Cancel Order после решения workflow.

#### Scenario: Ресторан отклонил заказ
**ID:** AC-109

- **WHEN** незавершённый `Order` получает событие отказа ресторана с причиной
- **THEN** `Order` сохраняет факт отказа и причину, остаётся в прежнем состоянии, а workflow получает факт отказа

### Requirement: Факты Preparation и Delivery отражаются один раз
**ID:** FR-107

Record Preparation Started, Record Order Ready, Record Courier Assignment, Record Pickup и Record Delivery Completion SHALL сохранять в незавершённом `Order` соответствующий факт со временем события не более одного раза, не меняя Order State и не навязывая порядок фактов, принадлежащий соседним контекстам.

#### Scenario: Отражение факта fulfillment
**ID:** AC-110

- **WHEN** незавершённый `Order` получает событие начала Preparation, готовности к Pickup, Courier Assignment, Pickup или Delivery Completion
- **THEN** `Order` сохраняет соответствующий факт со временем события, состояние не меняется, а workflow получает факт с исходным `event_id`

### Requirement: Неудача доставки сохраняется с причиной
**ID:** FR-108

Record Delivery Failure SHALL сохранять в незавершённом `Order` факт неудачи Delivery и доступную причину не более одного раза, не финализируя `Order`.

#### Scenario: Доставка не удалась
**ID:** AC-111

- **WHEN** незавершённый `Order` получает событие неудачи Delivery с причиной
- **THEN** `Order` сохраняет факт и причину, состояние не меняется, а workflow получает факт неудачи

### Requirement: Позднее событие для финализированного Order диагностируемо игнорируется
**ID:** FR-109

Если `Order` уже находится в `completed` или `cancelled`, входящий бизнес-факт MUST NOT изменять `Order`. Система SHALL фиксировать `event_id` в Inbox, возвращать результат «проигнорировано как позднее» и не считать отсутствие workflow ошибкой.

#### Scenario: Событие после финализации
**ID:** AC-112

- **WHEN** событие ресторана или доставки приходит для `Order` в `completed` или `cancelled`
- **THEN** `Order` не изменяется, `event_id` зафиксирован, результат помечен как позднее событие, а завершённый workflow не приводит к ошибке

### Requirement: Workflow уведомляется только после commit
**ID:** FR-110

Передача бизнес-факта Order Fulfillment Workflow SHALL выполняться после успешного commit локальной транзакции. Ошибка уведомления MUST возвращаться вызывающему, чтобы событие было доставлено повторно; откат транзакции MUST NOT сопровождаться уведомлением.

#### Scenario: Уведомление workflow не удалось
**ID:** AC-113

- **WHEN** commit успешен, а передача факта workflow завершается ошибкой
- **THEN** изменения `Order` и Inbox остаются зафиксированными, handler возвращает ошибку, а повторная доставка как duplicate повторяет уведомление

#### Scenario: Транзакция откатилась
**ID:** AC-114

- **WHEN** сохранение `Order` в транзакции завершается ошибкой
- **THEN** Inbox и `Order` не изменяются и workflow не уведомляется

### Requirement: Cancellation Request требует authorization
**ID:** FR-111

Request Order Cancellation SHALL выполнять authoritative check права `cancel` verified subject над `Order` до загрузки доменного состояния. Отказ или ошибка проверки MUST приводить к отказу без передачи запроса workflow.

#### Scenario: Subject не имеет права
**ID:** AC-115

- **WHEN** subject без права `cancel` или при недоступной authorization запрашивает отмену
- **THEN** система возвращает отказ доступа, не раскрывает состояние `Order` и не передаёт запрос workflow

### Requirement: Cancellation Request проверяется доменом и передаётся workflow
**ID:** FR-112

После authorization система SHALL проверять доменную допустимость Order Cancellation по Order State и окну отмены, полученному из cancellation policy, и передавать запрос существующему workflow с идентификатором запроса и исходным subject. Сам use case MUST NOT менять Order State и выполнять компенсации.

#### Scenario: Отмена допустима
**ID:** AC-116

- **WHEN** авторизованный subject запрашивает отмену `Order` в `placed` или `confirmed` внутри окна отмены
- **THEN** workflow получает Cancellation Request с идентификатором запроса и subject, а `Order` остаётся в прежнем состоянии

#### Scenario: Order уже завершён
**ID:** AC-117

- **WHEN** запрашивается отмена `Order` в `completed`
- **THEN** система возвращает конфликт состояния и не передаёт запрос workflow

#### Scenario: Окно отмены истекло
**ID:** AC-118

- **WHEN** момент запроса находится вне окна, заданного cancellation policy
- **THEN** система возвращает доменный отказ с причиной окна отмены и не передаёт запрос workflow

#### Scenario: Order уже отменён
**ID:** AC-119

- **WHEN** запрашивается отмена `Order` в `cancelled`
- **THEN** система возвращает результат «уже отменён» без ошибки и без нового запроса workflow

### Requirement: Complete Order финализирует подтверждённый Order один раз
**ID:** FR-113

Complete Order SHALL переводить `Order` из `confirmed` в `completed` и атомарно записывать `OrderCompleted` в Outbox. Повтор после успешного commit MUST быть no-op без второй записи Outbox; переход из другого состояния MUST отклоняться конфликтом состояния без изменений.

#### Scenario: Успешная финализация
**ID:** AC-120

- **WHEN** workflow вызывает Complete Order для `Order` в `confirmed`
- **THEN** `Order` переходит в `completed`, а `OrderCompleted` записывается в Outbox той же транзакции

#### Scenario: Повтор Activity после commit
**ID:** AC-121

- **WHEN** Complete Order повторяется для уже `completed` `Order`
- **THEN** результат отмечает уже применённый итог, а `Order` и Outbox не изменяются

#### Scenario: Недопустимое состояние
**ID:** AC-122

- **WHEN** Complete Order вызывается для `Order` в `placed` или `cancelled`
- **THEN** система возвращает конфликт состояния и ничего не сохраняет

### Requirement: Fail or Cancel Order фиксирует отрицательный итог с причиной
**ID:** FR-114

Fail or Cancel Order SHALL переводить незавершённый `Order` в `cancelled`, сохраняя вид итога (Cancellation или Failure), код причины и время решения, и атомарно записывать `OrderCancelled` в Outbox. Повтор MUST быть no-op; вызов для `completed` MUST отклоняться конфликтом состояния.

#### Scenario: Отрицательный итог зафиксирован
**ID:** AC-123

- **WHEN** workflow вызывает Fail or Cancel Order для `Order` в `placed` или `confirmed` с видом итога и причиной
- **THEN** `Order` переходит в `cancelled` с сохранёнными видом итога и причиной, а `OrderCancelled` записывается в Outbox

#### Scenario: Повтор после commit
**ID:** AC-124

- **WHEN** Fail or Cancel Order повторяется для уже `cancelled` `Order`
- **THEN** результат отмечает уже применённый итог без изменения `Order` и Outbox

#### Scenario: Order уже завершён успешно
**ID:** AC-125

- **WHEN** Fail or Cancel Order вызывается для `completed` `Order`
- **THEN** система возвращает конфликт состояния и ничего не сохраняет

### Requirement: Get Order возвращает доступное представление
**ID:** FR-115

Get Order SHALL проверять право `view` verified subject над `Order` до чтения read model и возвращать представление заказа без изменения состояния. Отказ доступа MUST возвращаться независимо от существования `Order`. Для `Order`, Order State которого допускает Order Cancellation, представление SHALL содержать окно отмены `opens_at`/`closes_at` из той же cancellation policy, что использует Request Order Cancellation; для `cancelled` и `completed` окно MUST отсутствовать.

#### Scenario: Авторизованное чтение
**ID:** AC-126

- **WHEN** subject с правом `view` запрашивает `Order`
- **THEN** система возвращает состав, total, Delivery Address, Order State, отражённые факты и причину отрицательного итога, если она есть

#### Scenario: Чтение без права или несуществующего заказа
**ID:** AC-127

- **WHEN** subject без права `view` запрашивает существующий или несуществующий `Order`
- **THEN** система возвращает отказ доступа; при наличии права и отсутствии `Order` — ошибку «не найден»

#### Scenario: Окно отмены незавершённого заказа
**ID:** AC-504

- **WHEN** subject с правом `view` запрашивает `Order` в состоянии `placed` или `confirmed`
- **THEN** представление содержит `opens_at`, равный моменту Place Order, и `closes_at`, равный `opens_at` плюс настроенная длительность окна

#### Scenario: Завершённый заказ без окна
**ID:** AC-505

- **WHEN** subject с правом `view` запрашивает `Order` в состоянии `cancelled` или `completed`
- **THEN** представление не содержит окна отмены

### Requirement: List Customer Orders возвращает историю доступному actor
**ID:** FR-116

List Customer Orders SHALL возвращать заказы одного Customer от новых к старым постранично. Customer MUST получать собственный список фильтрацией по владельцу без authorization graph; другой subject SHALL проходить authoritative check права просмотра заказов Customer.

#### Scenario: Customer читает свою историю
**ID:** AC-128

- **WHEN** Customer запрашивает свои заказы с ограничением размера страницы
- **THEN** система возвращает только его заказы от новых к старым и курсор следующей страницы, если она есть

#### Scenario: Чужая история
**ID:** AC-129

- **WHEN** другой subject запрашивает заказы Customer
- **THEN** без права просмотра система возвращает отказ доступа, а при наличии права — список этого Customer

### Requirement: Конкурентное изменение Order обнаруживается по версии
**ID:** FR-117

Каждый Command, изменяющий `Order`, SHALL сохранять его с условием на загруженную `version`. Несовпадение версии MUST приводить к типизированной ошибке конфликта и откату всех изменений транзакции, включая Inbox и Outbox.

#### Scenario: Параллельное изменение
**ID:** AC-130

- **WHEN** `Order` изменён другой транзакцией между загрузкой и сохранением
- **THEN** handler возвращает ошибку конфликта версии, а `Order`, Inbox и Outbox остаются без изменений

### Requirement: Cancellation policy задаётся конфигурацией
**ID:** FR-462

Production cancellation policy SHALL открывать окно в момент Place Order и закрывать его через `cancellation.window`. Неположительная или неразбираемая длительность MUST останавливать процесс на старте, а тестовые адаптеры `AlwaysOpen` и `FixedWindow` MUST NOT использоваться в production composition root.

#### Scenario: Неверная длительность окна
**ID:** AC-506

- **WHEN** процесс `orders` стартует с `CANCELLATION__WINDOW=0s`
- **THEN** загрузка конфигурации завершается ошибкой, и процесс не начинает обслуживать запросы

### Requirement: Ошибка cancellation policy не ломает Get Order
**ID:** FR-463

Если cancellation policy возвращает ошибку, Get Order SHALL возвращать представление без окна отмены и MUST фиксировать ошибку в log и trace.

#### Scenario: Политика недоступна
**ID:** AC-507

- **WHEN** cancellation policy возвращает ошибку для `Order` в состоянии `placed`
- **THEN** Get Order возвращает представление заказа без окна отмены, а ошибка записана в log

### Requirement: Place Order доступен через gRPC
**ID:** FR-464

`OrdersService.PlaceOrder` SHALL вызывать Place Order с Customer, равным verified subject вызова, и MUST NOT принимать customer из полей запроса. Ошибки SHALL маппиться в `google.rpc.ErrorInfo` домена `nomnom.orders.v1`.

#### Scenario: Оформление заказа клиентом
**ID:** AC-508

- **WHEN** verified user subject вызывает `PlaceOrder` с доступными позициями
- **THEN** ответ содержит `order_id`, `replayed=false`, `fulfillment_started=true`, а повтор с тем же `idempotency_key` возвращает тот же `order_id` с `replayed=true`

#### Scenario: Меню ресторана недоступно
**ID:** AC-509

- **WHEN** catalog snapshot нельзя получить, в том числе в production до API меню `restaurants`
- **THEN** `PlaceOrder` возвращает `UNAVAILABLE` с reason `DEPENDENCY_UNAVAILABLE`, и `Order` не создаётся

#### Scenario: Service subject не Customer
**ID:** AC-510

- **WHEN** `PlaceOrder` вызывает service subject
- **THEN** вызов завершается `PERMISSION_DENIED`

#### Scenario: Некорректный состав или адрес
**ID:** AC-511

- **WHEN** запрос не содержит позиций, повторяет `menu_item_id`, содержит неположительное количество или пустой Delivery Address
- **THEN** вызов завершается `INVALID_ARGUMENT` с reason `INVALID_ORDER`

### Requirement: Request Order Cancellation доступен через gRPC
**ID:** FR-465

`OrdersService.RequestOrderCancellation` SHALL вызывать Request Order Cancellation с verified subject и той же cancellation policy, что и Get Order. Недопустимое по state или окну требование и отказ workflow MUST возвращать `FAILED_PRECONDITION`/`ORDER_STATE_CONFLICT`; отсутствие workflow и недоступность SpiceDB — `UNAVAILABLE`/`DEPENDENCY_UNAVAILABLE`.

#### Scenario: Запрос передан workflow
**ID:** AC-512

- **WHEN** customer запрашивает отмену заказа в окне отмены
- **THEN** ответ содержит outcome `REQUESTED`, а для уже отменённого заказа — `ALREADY_CANCELLED`

#### Scenario: Ошибки отмены
**ID:** AC-513

- **WHEN** заказ завершён, окно закрыто, workflow отказал, workflow не найден или SpiceDB недоступен
- **THEN** вызов завершается соответственно `FAILED_PRECONDITION` (первые три случая) или `UNAVAILABLE`

### Requirement: Зависимости command RPC в API
**ID:** FR-466

Процесс API SHALL использовать lazy Temporal client, системный clock и UUIDv7 генератор `order_id`; старт и readiness MUST NOT зависеть от доступности Temporal. Тестовые адаптеры `memory.*` MUST NOT использоваться в production composition root.

#### Scenario: Temporal недоступен при старте
**ID:** AC-514

- **WHEN** API собирается с недостижимым `TEMPORAL__HOST_PORT`
- **THEN** сборка завершается успешно, а ошибку получает только вызов, которому нужен workflow

#### Scenario: Идентификатор заказа
**ID:** AC-515

- **WHEN** Place Order создаёт `Order`
- **THEN** `order_id` — UUIDv7

### Requirement: Конкурентный повтор Place Order возвращает зафиксированный Order
**ID:** FR-565

Если два запроса Place Order одного Customer с одним idempotency key выполняются одновременно и второй обнаруживает key, занятый закоммиченным Order первого, при сохранении key, Place Order SHALL откатить свою транзакцию, перечитать key после commit первого и вернуть его `order_id` с `replayed=true`. Если Order по key не найден, запрос MUST завершаться конфликтом конкурентности (gRPC `ABORTED`), а не внутренней ошибкой. Relationship второго запроса остаётся reconciliation.

#### Scenario: Одновременные запросы с одним ключом
**ID:** AC-671

- **WHEN** второй запрос с тем же idempotency key сохраняет key, пока первый не закоммичен, и первый затем коммитит свой Order
- **THEN** второй запрос возвращает `order_id` первого с `replayed=true` и не создаёт второй `Order`
