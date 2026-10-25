# 22. Specification Pattern for Domain Policies

Date: 2026-10-24

## Status

Accepted

Связан с [6. DDD Structure Inside Vertical Slices](0006-ddd-structure-inside-vertical-slices.md), [18. SpiceDB for Authorization](0018-spicedb-for-authorization.md), [19. BDD for Business Acceptance Testing](0019-bdd-for-business-acceptance-testing.md) и [21. Pure FSM Transitions](0021-pure-fsm-transitions.md).

## Context

Некоторые доменные условия имеют самостоятельный бизнес-смысл и повторяются в нескольких операциях: cancellation window, часы работы ресторана, capacity, зона доставки, лимит суммы и срок authorization. Если оставлять такие правила внутри handlers или размножать по domain methods, они расходятся, плохо комбинируются и получают разные причины отказа.

При этом не каждое условие является Specification. [ADR 21](0021-pure-fsm-transitions.md) уже отдаёт допустимость изменения business state единой transition table FSM. [ADR 6](0006-ddd-structure-inside-vertical-slices.md) оставляет canonical aggregates и бизнес-инварианты в domain layer. [ADR 18](0018-spicedb-for-authorization.md) отделяет authorization через SpiceDB от domain model, а [ADR 19](0019-bdd-for-business-acceptance-testing.md) требует проверять значимое поведение бизнес-языком без копирования механизма реализации.

Нужна явная граница, которая позволяет переиспользовать содержательные eligibility/policy/constraint rules, но не превращает каждый `if`, input validation, state transition или query predicate в framework abstraction.

На этом шаге создаются только решение и документация. Specifications, composite framework и изменения application/domain code не реализуются.

## Decision

Использовать Specification Pattern для чистых доменных правил с самостоятельным бизнес-смыслом, которые относятся к eligibility, policy или constraint и действительно переиспользуются, комбинируются либо независимо тестируются. Подробные правила и кандидаты описаны в [документации Specification Pattern](../specification-pattern.md).

### Граница с FSM и другими правилами

Допустимость действия по текущему business state aggregate определяется только transition table FSM. Specification применяется только к условиям вне state: времени, сумме, зоне, capacity, расписанию и лимитам. Одно правило не кодируется одновременно в FSM и spec.

Input validation остаётся на transport/application boundary или в constructor, value-object invariant — в value object, aggregate invariant — в aggregate. Domain Specification не заменяет эти механизмы и не используется для извлечения всех инвариантов в отдельные классы.

Repository/query specification (`OrdersByCustomer`, SQL/ORM predicate, sorting) является отдельным понятием persistence/query layer. Domain spec не транслируется автоматически в SQL или MongoDB filter.

### Размещение и владение поведением

Spec, используемая внутри aggregate behavior, располагается рядом с canonical aggregate в `domains/` владеющего slice согласно ADR 6. Spec только одного slice остаётся в его `domains/`. Правило над несколькими aggregates оформляется domain service внутри владеющего boundary: application загружает данные, а service вызывает spec. Specs не копируются между use-case packages или slices.

Aggregate остаётся владельцем поведения и инвариантов. Если policy относится к `order.Cancel(...)`, application вызывает метод aggregate, а не spec напрямую и не меняет state вручную. Aggregate вычисляет вне-state Specification, затем передаёт trigger в FSM и применяет результат перехода.

### Чистота и входы

Specification зависит только от domain data и явно переданных значений. Repository, DB, Kafka, Inbox/Outbox, Temporal, SpiceDB, gRPC/HTTP, OpenTelemetry, system clock, filesystem, network и DI запрещены.

Application получает внешние данные и время через ports/clock и передаёт их domain model значениями: отдельными типизированными параметрами либо immutable context-struct. Не используются `map[string]any`, service locator и скрытое чтение global clock.

### Результат и композиция

Spec возвращает `satisfied` либо `unsatisfied(reasons)`, а не только boolean. Reasons являются типизированными domain values. Aggregate преобразует unsatisfied result в типизированную domain error; infrastructure маппит её в gRPC/GraphQL/HTTP contract, а UI выбирает пользовательское сообщение по публичной бизнес-причине.

Именованные specs могут образовывать `AND`, `OR` и `NOT`. Композиция сохраняет причины: `AND` собирает причины всех неисполненных условий, `OR` — причины всех веток, если ни одна не удовлетворена, `NOT` получает явную reason именованного отрицательного правила. Универсальный expression engine не вводится.

### Naming и форма реализации

Используются бизнес-имена: `PaymentRefundable`, `CourierEligibleForDelivery`, `WithinCancellationWindow`. Технические имена вроде `OrderStatusSpecification` и `GenericSpecification` запрещены.

Начальная форма — простая функция или идиоматичный тип языка, например `func Refundable(payment Payment, now time.Time) Result`. Иерархии `AbstractSpecification<T>`/`CompositeSpecification<T>`, DSL и framework abstractions допустимы только после доказанного повторного использования.

### Authorization и testing

SpiceDB отвечает за право subject выполнить действие и вызывается application layer через authorization port. Permission checks, relations и ZedToken не являются domain Specifications и не входят в их композицию.

Specs тестируются полностью in-memory: positive, negative, boundary cases, композиция и точные reasons. BDD описывает бизнес-правило без упоминания имени spec или механизма композиции.

## Consequences

Переиспользуемые policies получают одно доменное имя, одну реализацию и стабильные причины отказа. Их можно независимо тестировать и безопасно комбинировать, не затягивая infrastructure в domain model. Разделение с FSM сохраняет transition table единственным источником state-based допустимости.

Application обязан заранее получать `now` и внешние snapshots и передавать их как значения. Aggregate/domain service сохраняет ответственность за действие, поэтому spec нельзя использовать как shortcut для ручного изменения state в handler.

Паттерн создаёт риск over-engineering и появления generic framework. Его ограничивают требование самостоятельного бизнес-смысла, создание specs только по SDD и предпочтение простых функций. Отдельно требуется следить, чтобы state checks, authorization и persistence predicates не маскировались под domain Specifications.
