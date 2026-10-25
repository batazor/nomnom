# Specification Pattern

Specification выражает содержательное доменное правило в форме вопроса: удовлетворяет ли объект бизнес-условию? Паттерн применяется к eligibility, policy и constraint, когда правило имеет самостоятельный смысл, переиспользуется, комбинируется или тестируется независимо.

Specification не является обязательной оболочкой для каждого условия. Простое правило остаётся обычной функцией или идиоматичным типом языка, пока реальное переиспользование не требует композиции. Архитектурное решение зафиксировано в [ADR 22: Specification Pattern for Domain Policies](adr/0022-specification-pattern-for-domain-policies.md).

## Граница с FSM

Главное правило разделения:

- допустимость действия из текущего business state агрегата определяется только transition table FSM;
- Specification выражает условия вне state: временное окно, сумму, географическую зону, capacity, расписание, лимит или явно переданный snapshot внешних бизнес-данных.

Одно правило не кодируется одновременно в transition table и Specification. Например, `Payment` можно refund только из `captured` — это переход FSM. Условие «refund запрошен не позднее 30 дней и не превышает доступную к возврату сумму» — `PaymentRefundable`.

Specification может служить чистым механизмом вне-state guard: aggregate method вычисляет spec до FSM trigger либо transition guard делегирует правило spec. Выбирается одно место вызова, чтобы правило и его причина не дублировались. Подробная граница со state transitions описана в [документации FSM](fsm.md#specification-и-вне-state-guards).

## Когда создавать Specification

Specification оправдана, если правило одновременно:

- имеет имя на языке домена;
- отвечает на вопрос eligibility/policy/constraint без изменения объекта;
- использует domain data и переданные значения;
- переиспользуется, независимо тестируется или осмысленно комбинируется с другими правилами.

Не следует извлекать в specs все проверки и инварианты:

| Правило | Владелец |
| --- | --- |
| Required field, формат UUID, `quantity > 0` | Input validation / constructor |
| `Money ≥ 0` | Value object |
| `captured ≤ authorized` | Aggregate invariant |
| Разрешён ли trigger из текущего state | FSM transition table |
| Срок, сумма, зона, capacity, расписание, лимит | Domain Specification |
| Может ли subject выполнить действие | Authorization port / SpiceDB |
| `OrdersByCustomer`, sorting, pagination, SQL predicate | Query/persistence layer |

Repository/query specification — отдельное техническое понятие. Domain Specification не становится query object и не транслируется автоматически в SQL, MongoDB filter или ORM expression.

## Размещение

Размещение следует владению правила и структуре canonical model из [ADR 6](adr/0006-ddd-structure-inside-vertical-slices.md):

- spec, используемая поведением aggregate, находится рядом с canonical aggregate в `<boundary>/slices/<slice>/domains/<aggregate>/` либо непосредственно в `domains/`, если aggregate не выделен в подкаталог;
- spec, которую вызывает только application одного slice и которая не принадлежит поведению aggregate, находится в `<boundary>/slices/<slice>/domains/`;
- правило над несколькими aggregates оформляется domain service внутри владеющего boundary; service собирает уже загруженные aggregates или чистые значения и вызывает spec;
- spec не копируется между use-case packages или slices; несколько use cases одного slice используют его canonical `domains/`;
- одинаковое имя в разных bounded contexts не создаёт общую межконтекстную spec.

Domain service не загружает aggregates самостоятельно. Application handler загружает их через repositories, получает внешние значения, затем передаёт всё domain service. Если правило действительно пересекает существующие slices и не имеет очевидного владельца, сначала определяется cohesive domain module в рамках ADR 6, а не создаётся общий каталог helpers.

## Чистота и входные данные

Specification детерминирована относительно входов. Она использует только domain data и явно переданные значения. Внутри запрещены:

- repository, database и persistence models;
- Kafka, Inbox и Outbox;
- Temporal, gRPC/HTTP и другие transports;
- SpiceDB и authorization client;
- OpenTelemetry, logging и metrics;
- system clock, filesystem, network и random;
- DI container и service locator.

Application получает внешние данные и `now`, преобразует их в domain values и передаёт явными параметрами либо immutable context-struct. Контекст получает бизнес-имя и типизированные поля, например `RefundPolicyContext{Now, CapturedAt, RequestedAmount}`; `map[string]any` и аналоги не используются.

## Результат с причинами

Specification возвращает не только boolean, а доменный результат:

```text
satisfied
unsatisfied(reasons)
```

Reasons — типизированные стабильные domain reasons, пригодные для программной обработки. Это не готовые gRPC status, GraphQL error, HTTP response или локализованный UI-текст.

Aggregate остаётся владельцем поведения. Он преобразует `unsatisfied` в типизированную domain error, например `ErrOrderCannotBeCancelled{Reasons}`. Infrastructure сопоставляет domain/application error с transport contract; UI по публичному reason code выбирает понятное пользователю сообщение.

## Композиция

Композиция сохраняет язык домена. Например:

```text
CourierEligibleForDelivery =
  CourierAvailable
  AND CourierWithinDeliveryZone
  AND NOT CourierHasActiveAssignment
```

В текущей модели `Courier.status` является business state. Поэтому `CourierAvailable` не дублирует проверку `status == available` внутри spec: state-based допустимость назначения остаётся у поведения `Courier`/FSM. Компонент с таким именем допустим только если он означает отдельную операционную availability policy, не совпадающую со state. Вне-state часть текущего кандидата — зона, дистанция и активные назначения.

Семантика причин:

- `AND` вычисляет составляющие и собирает причины всех unsatisfied правил;
- `OR` удовлетворён, если удовлетворена хотя бы одна ветка; если не удовлетворена ни одна, собирает причины всех веток;
- `NOT` инвертирует результат, а для отрицательного результата использует явную business reason самой именованной композиции, а не механически превращает текст внутреннего правила.

Универсальный expression engine, AST, reflection-based DSL и обязательная иерархия `AbstractSpecification<T>` не вводятся. В Go предпочтительна функция вида `func Refundable(payment Payment, now time.Time) Result`; в остальных языках используются столь же простые функции, callable types или небольшие domain types. Composite abstraction появляется только после доказанного повторного использования `AND/OR/NOT`.

## Domain methods

Если правило принадлежит поведению aggregate, application не вызывает spec вместо aggregate и не меняет state вручную:

```text
application:
  now = clock.Now()
  order = repository.Load(orderID)
  result = order.Cancel(now, cancellationPolicy)

aggregate:
  specResult = WithinCancellationWindow(order, now, cancellationPolicy)
  if unsatisfied: return ErrOrderCannotBeCancelled{reasons}
  transition = FSM.Trigger(Cancel)
  update state
  return domain result
```

Spec выражает правило, но не выполняет действие, не сохраняет aggregate и не подменяет его инварианты. Application может вызывать spec напрямую только для application-only policy, которая не является частью поведения aggregate, например для выбора кандидатов перед вызовом domain service.

## Authorization

Specification отвечает на бизнес-вопрос об объекте, а SpiceDB — на вопрос о праве subject выполнить действие над resource. Проверка authorization выполняется application layer через authorization port до domain operation. Subject permissions, SpiceDB relations, ZedToken и consistency не передаются в domain spec.

Если два условия нужны одному use case, они остаются раздельными:

```text
authorize(subject, action, resource)
→ load domain data
→ aggregate method / domain specification
```

Отказ authorization не маскируется domain reason, а unsatisfied domain policy не превращается в permission denial.

## Первые Specifications

Начальный набор реализован как простые чистые функции/типы с типизированными reasons и in-memory unit tests. Их подключение к aggregate methods, domain services и application use cases выполняется только вместе с соответствующим SDD; универсальный framework не вводится.

| Boundary | Candidate | Условия вне state |
| --- | --- | --- |
| `orders` | `WithinCancellationWindow` | `now` относительно времени оформления/подтверждения и заданной cancellation policy |
| `orders` | `OrderItemsOrderable` | snapshot доступности позиций, минимальная и максимальная сумма заказа, применимые лимиты |
| `restaurants` | `RestaurantAcceptingOrders` | часы работы, текущая capacity и расписание исключений |
| `restaurants` | `MenuItemAvailable` | окно доступности, остаток/capacity и временные ограничения предложения |
| `delivery` | `CourierEligibleForDelivery` | зона, расстояние, capacity и наличие активных назначений; state курьера проверяется отдельно |
| `delivery` | `DeliveryOfferAcceptable` | срок действия предложения, расстояние до pickup и лимиты активных назначений |
| `payments` | `PaymentRefundable` | запрошенная сумма, доступный остаток к возврату и срок после capture |
| `payments` | `PaymentCapturable` | сумма capture, доступный остаток authorization и срок её действия |

Подключение в application-слое (changes `implement-{orders,restaurants,delivery,payments}-application-layer`):

| Boundary | Specification | Где вычисляется |
| --- | --- | --- |
| `orders` | `OrderItemsOrderable` | Фабрика `order.Place` над catalog snapshot; отказ — `NotOrderable{Reasons}` |
| `orders` | `WithinCancellationWindow` | `order.CheckCancellation` перед dry-run строки FSM `cancel`; окно приходит из порта `CancellationPolicy` |
| `restaurants` | `RestaurantAcceptingOrders`, `MenuItemAvailable` | Domain service `evaluateOrderAcceptance` в `kitchenorder`; его результат — guard Accept |
| `delivery` | `CourierEligibleForDelivery` | Handler Find Courier для каждого кандидата с параметрами `AssignmentPolicy` |
| `delivery` | `DeliveryOfferAcceptable` | `DeliveryOffer::accept` с `now` и snapshot курьера: адресат → specification → FSM |
| `payments` | `PaymentRefundable`, `PaymentCapturable` | Не подключены: authorization и refund windows не определены ([use cases](use-cases.md#open-questions)) |

`CanCompleteOrder`, `CanStartPreparation` и `CanAcceptRestaurantOrder` не являются specs, когда выражают только допустимость перехода из текущего state. Эти правила остаются в transition table соответствующей FSM.

Граничная семантика первой реализации:

- cancellation, offer, authorization и refund deadlines включают точный момент deadline;
- часы приёма заказов рестораном используют полуинтервал `[opensAt, closesAt)`;
- окно доступности Menu Item включает обе временные границы;
- maximum amount и maximum distance включены в допустимый диапазон;
- capacity считается исчерпанной при `current >= maximum`.

## Testing

Domain Specifications тестируются in-memory без mocks внешних систем:

- положительный результат;
- каждый отрицательный исход и точная reason;
- boundary values времени, суммы, расстояния, capacity и лимитов;
- композиции `AND`, `OR`, `NOT` и сбор причин;
- одинаковый результат для одинаковых domain inputs.

BDD описывает правило бизнес-языком, например «возврат отклонён после окончания окна», без имени класса или функции spec. Slice acceptance вызывает application interface и проверяет наблюдаемый бизнес-результат; unit tests самой Specification проверяют полный набор границ и reasons.
