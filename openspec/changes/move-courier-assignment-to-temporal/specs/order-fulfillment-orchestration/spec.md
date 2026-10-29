## ADDED Requirements

### Requirement: Ожидание назначения курьера без срока
**ID:** FR-709

После создания Delivery workflow SHALL ждать Courier Assignment без бизнес-таймера. Поиском курьера владеет `delivery`: workflow MUST NOT перепланировать доставку, MUST NOT отменять Order по сроку назначения и MUST NOT ждать решения оператора.

#### Scenario: Курьер назначен
**ID:** AC-002

- **WHEN** `orders` получает событие Courier Assignment
- **THEN** Inbox и application handler доставляют сигнал текущему workflow, который продолжает процесс без самостоятельного выбора Courier

#### Scenario: Курьера долго нет
**ID:** AC-968

- **WHEN** после Create Delivery курьер не назначен дольше любого прежнего срока назначения
- **THEN** workflow остаётся в ожидании Courier Assignment, не вызывает Activities `delivery` и не меняет Order

### Requirement: Решения процесса без replan принимает доменная модель
**ID:** FR-710

Правила Order Fulfillment — следующий шаг маршрута, реакция на факты соседей, сроки, допустимость Cancellation Request и план компенсации — SHALL реализовываться доменной моделью слайса `fulfillment`, которая MUST NOT зависеть от Temporal SDK, часов и случайности. Temporal workflow SHALL только переводить Signals и Updates в вызовы модели и исполнять её решения.

#### Scenario: Ожидание курьера проверяется без Temporal
**ID:** AC-969

- **WHEN** unit-тест модели создаёт Delivery и сдвигает время на сутки без Courier Assignment
- **THEN** модель не возвращает ни шага, ни deadline, а stage остаётся ожиданием курьера

#### Scenario: Компенсация по журналу шагов
**ID:** AC-547

- **WHEN** процесс с подтверждёнными Authorization и запросом в ресторан получает отказ ресторана
- **THEN** модель возвращает план компенсации из отмены Authorization и закрытия заказа как отказа, без отмены доставки, которая не создавалась

## MODIFIED Requirements

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
- **THEN** workflow не игнорирует событие молча, а запускает предусмотренную Saga с диагностикой

## REMOVED Requirements

### Requirement: Назначение курьера допускает replan и решение оператора

**Reason**: продуктово ожидание курьера не ограничено сроком: курьер рано или поздно найдётся, а оператор доставку не выполнит. Поиском целиком владеет Assignment Workflow `delivery`.

**Migration**: заменено FR-709; сценарий AC-002 перенесён туда. Signal `OperatorDecision` и stage `waiting_operator` удаляются.

### Requirement: Assignment допускает replan и решение оператора
**ID:** FR-417

**Reason**: replan и эскалация оператору при истечении deadline Assignment удаляются вместе со сроком назначения.

**Migration**: `FULFILLMENT__ASSIGNMENT_TIMEOUT`, `FULFILLMENT__MAX_REPLANS`, `FULFILLMENT__OPERATOR_DECISION_TIMEOUT`, шаг и Activity `ReplanDelivery`, причина `operator_decision` удаляются. AC-437 больше не применим. Workflow, запущенные прежним кодом, не переносятся: kind сбрасывается.

### Requirement: Решения процесса принимает доменная модель
**ID:** FR-483

**Reason**: требование перечисляло replan и эскалацию среди правил модели; они удаляются.

**Migration**: заменено FR-710 с тем же правилом без replan и эскалации; AC-547 перенесён туда, AC-546 заменён AC-969.
