# Spec Delta

## ADDED Requirements

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
