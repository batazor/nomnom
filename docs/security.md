# Security: authentication and authorization

Документ определяет общесистемный contract authentication context и authorization в BiteRoute. Архитектурное решение зафиксировано в [ADR 18](adr/0018-spicedb-for-authorization.md). Конкретная `.zed` schema, deployment SpiceDB, datastore, SDK-интеграция и manifests относятся к следующим шагам.

## Разделение ответственности

| Ответственность | Ответ |
| --- | --- |
| Authentication | Кто выполняет запрос? Проверка credentials/token и установление identity. |
| Authorization | Может ли subject выполнить permission над конкретным resource? |
| Domain rules | Допустима ли операция в текущем состоянии агрегата? |

SpiceDB решает только authorization. Он не является identity provider, не выполняет login, не хранит sessions и не выпускает access tokens. Каждый bounded context принимает окончательное решение о защищённом use case.

```text
authenticated subject
        |
        v
authorization: Can subject perform permission on resource?
        |
        v
domain: Is the operation valid in the current state?
        |
        v
persist state
```

Authorization выполняется раньше доменных проверок. `PERMISSION_DENIED` не раскрывает, в каком состоянии находится скрытый resource, и не меняет domain state.

## Subject context

В authorization graph используются:

- `user:<id>` — конечный пользователь;
- `service:<id>` — конкретная workload identity BFF, API, Temporal Activity worker, Kafka consumer или фоновой задачи.

Общего всемогущего `system` subject нет. System operation выполняется от конкретной `service` identity с минимально необходимыми permissions.

BFF проверяет внешний access token и формирует subject context. Bounded context принимает его только через проверяемый механизм:

1. BFF передаёт исходный access token, и владелец use case самостоятельно проверяет token; либо
2. BFF выпускает короткоживущую подписанную internal identity, и владелец проверяет signature, issuer, audience, expiry и replay-related claims.

Обычный header `x-user-id`, `x-role`, `x-tenant` или аналогичное значение не является доказательством identity. Internal gateway обязан сохранить authorization metadata, но факт прохождения через gateway и NetworkPolicy не делает её доверенной. Для пользовательского subject выбор между propagated token и signed internal identity остаётся открытым до проектирования BFF identity flow.

### Service identity

`service` subject между bounded contexts доказывается Kubernetes projected ServiceAccount token ([ADR 27](adr/0027-service-identity-via-projected-serviceaccount-tokens.md)): metadata `authorization: Bearer <jwt>`, audience целевого контекста (`nomnom.orders`, `nomnom.restaurants`, `nomnom.delivery`, `nomnom.payments`), подпись по JWKS кластера, `iss`, `exp`/`nbf` с допуском 30 s и allowlist `namespace/serviceaccount=service-name`. Любое нарушение — `UNAUTHENTICATED`; без `IDENTITY__ISSUER`, `IDENTITY__AUDIENCE` и `IDENTITY__JWKS_FILE` сервер отклоняет всех (fail closed). Resolver только аутентифицирует: permissions identity по-прежнему проверяет SpiceDB в use case. Правила проверки во всех языках закреплены общими векторами `tests/identity`.

### Асинхронные операции

Temporal Activities, Kafka consumers и background jobs используют `service` identity. Они не восстанавливают и не impersonate пользователя из сохранённого идентификатора.

Если пользователь ранее инициировал действие, например запросил отмену заказа, API:

1. аутентифицирует пользователя;
2. проверяет его permission при принятии команды;
3. сохраняет исходный subject в данных операции для audit/correlation;
4. запускает дальнейшую асинхронную работу.

Workflow или consumer выполняет последующие шаги от своей `service` identity. Исходное разрешение пользователя не проверяется повторно при каждом retry: retry не является новой пользовательской командой.

## ReBAC model

SpiceDB хранит graph:

```text
subject → relationship → resource → permission
```

Вызов приложения имеет форму:

```text
Can <subject> perform <permission> on <resource-type>:<resource-id>?
```

Naming convention:

- relations — существительные: `owner`, `staff`, `customer`, `courier`;
- permissions — глаголы из SDD use cases: `view`, `cancel`, `accept`, `assign`;
- роли не передаются вручную по цепочке вызовов, если выражаются graph relations/permissions.

Начальное направление моделирования, не готовая schema:

```text
user, service
restaurant: owner, staff
order: customer, restaurant
delivery: courier, order
```

Definitions и permissions проектируются по согласованным SDD use cases. Текущие спецификации описывают инфраструктурные сценарии и не дают достаточного набора доменных use cases для окончательной schema.

## Schema как shared contract

Единая schema системы хранится в [`authorization/schema.zed`](../authorization/schema.zed), grants развёртывания — в [`authorization/bootstrap.yaml`](../authorization/bootstrap.yaml), assertions путей пользовательских прав — в [`authorization/validation.yaml`](../authorization/validation.yaml) (`make authz-validate`, локальный SpiceDB — `make spicedb-dev`). Команды Order Fulfillment Workflow — capabilities синглтона `platform:nomnom`, выданные relation `fulfillment@service:orders-worker`: worker создаёт и адресует ресурсы по `order_id` раньше, чем у них появляются relationships. Пользовательские права остаются resource-level. Соответствие портов контекстов и permissions — в [design change `add-spicedb-authorization`](../openspec/changes/archive/2026-10-25-add-spicedb-authorization/design.md#соответствие-портов-и-permissions-нормативно).

Каждый resource definition имеет owner:

| Resource | Owner |
| --- | --- |
| `restaurant` | `restaurants` |
| `order` | `orders` |
| `delivery` | `delivery` |
| `customer` | `orders`; permission `view_orders` защищает историю заказов Customer, id совпадает с id `user` |
| `payment` | `payments`; permissions application-слоя: `create`, `authorize`, `capture`, `cancel_authorization`, `refund`, `view`, `view_operations` |
| `user`, `service` | Общесистемный identity contract; lifecycle определяется вместе с authentication/workload identity. |

Порты authorization контекстов (SpiceDB-адаптеры переводят их в definitions `schema.zed`):

| Boundary | Где объявлены | Что используется |
| --- | --- | --- |
| `orders` | Константы use-case packages и `AccessCheck` в `pkg/application` | `view` и `cancel` на `order:<id>`, `view_orders` на `customer:<id>` для чужой истории заказов |
| `restaurants` | Ports `RestaurantAccessControl`, `MenuAccessControl`, `KitchenAccessControl` в `applications/access-control.ts` слайсов | Методы выражают нужду use case (`canManageRestaurant`, `canManageMenu`, `canOperateKitchen`, …), а не имя permission |
| `delivery` | Ports `CourierAuthorization`, `DeliveryAuthorization` с enum permissions по use cases | Resources `Platform`, `Courier(id)`, `Delivery(id)` |
| `payments` | `PaymentPermission` и `PaymentPermissionChecker` в `slices/payment/applications/ports.py` | Permissions из строки `payment` выше над `PaymentResource(payment_id)` |

Сопоставление с definitions `schema.zed` выполняется при её создании. Provider callback и operation result в `payments` не проходят SpiceDB-проверку: actor не является subject authorization graph, подлинность callback проверяет infrastructure (service-path; см. [design.md change `implement-payments-application-layer`](../openspec/changes/implement-payments-application-layer/design.md)). Решения по каждому контексту — в design.md changes `implement-{orders,restaurants,delivery,payments}-application-layer`.

Изменения schema проходят review с участием владельцев затронутых definitions и consumers permissions. CI должен выполнять:

- `zed validate authorization/schema.zed`;
- assertions для granted и denied paths;
- regression cases для permissions, используемых приложениями.

Удаление или изменение relation/permission — breaking change. Процесс аналогичен Protobuf evolution:

1. добавить совместимую relation/permission;
2. развернуть schema;
3. перевести writers, relationships и checks;
4. подтвердить отсутствие старых callers/relationships;
5. удалить старый элемент отдельным reviewed change.

## Enforcement points

### BFF

BFF выполняет coarse-grained authorization: ограничивает endpoint/operation, раньше отклоняет заведомо запрещённый запрос и помогает фильтровать клиентский сценарий. Это defense in depth и оптимизация, а не окончательное разрешение state change.

### Bounded context

Владелец resource выполняет authoritative check перед защищённым application use case даже при вызове от BFF:

```text
client → BFF → orders → CheckPermission(user:123, cancel, order:456)
                           → Order.Cancel()
                           → persist
```

Проверка доступа и domain rules остаются отдельными. Например, permission `cancel` разрешает попытку отмены, а `Order.Cancel()` отклоняет уже доставленный заказ.

## Размещение в Vertical Slice Architecture

```text
<slice>/
  domains/          # invariants; no SpiceDB types
  applications/     # authorization port beside the use case
  infrastructure/   # SpiceDB adapter/client
```

Authorization port является узким interface, выражающим нужду use case. SpiceDB adapter скрывает SDK, gRPC, resource-name mapping, consistency, retries, circuit breaker и telemetry. `domains/` не импортирует SpiceDB SDK, gRPC client, ZedToken или schema types.

## List use cases

`CheckPermission` не фильтрует коллекции. До реализации list use case выбирается стратегия:

| Условие | Стратегия |
| --- | --- |
| Владелец уже известен domain storage, например `customer_id = subject` | Фильтрация domain query; не копировать это знание в SpiceDB только ради списка. |
| Доступ следует cross-cutting graph, например staff ресторана или courier | `LookupResources` / `LookupSubjects`. |
| Кандидатная выборка мала и ограничена | `CheckBulkPermissions` для post-filter. |

Решение должно учитывать pagination, верхнюю границу кандидатов, latency и consistency. N одиночных checks на неограниченный список запрещены.

## Consistency и ZedToken

| Сценарий | Consistency |
| --- | --- |
| Обычный check | `minimize_latency` |
| Check должен увидеть собственный relationship write | `at_least_as_fresh` с ZedToken |
| Требуется самый новый снимок по обоснованной причине | `fully_consistent` |

Для `create → immediately check` применяется последовательность:

```text
begin local transaction
  allocate resource id
  TOUCH creation/owner relationship in SpiceDB
  receive ZedToken
  persist resource + authorization revision
commit local transaction
next check: at_least_as_fresh(revision)
```

Authorization revision сохраняется рядом с resource как техническое поле local storage в той же транзакции. Если следующий check выполняет другой bounded context в том же синхронном сценарии, ZedToken передаётся в gRPC metadata; internal gateway сохраняет metadata без изменения. Metadata key — `authorization-revision` (значение — ZedToken как есть). Сервер применяет его к проверкам `platform:nomnom` и ресурсов без собственной сохранённой revision; сохранённая revision ресурса приоритетнее. Token влияет только на свежесть ответа и никогда не расширяет права; пустое значение или значение длиннее 1024 символов игнорируется. Реализовано в `restaurants`.

`fully_consistent` не является способом скрыть отсутствие lifecycle design: он дороже и используется только с явным обоснованием.

Реализация: `orders` хранит revision `order#customer` в строке `Order`; `restaurants` — revision `restaurant#owner/staff` в `Restaurant`; `delivery` — revision `courier#self` в `Courier` и `delivery#courier` в `Delivery`. Revision сохраняется и после `DELETE` (Revoke Staff, Reassign Courier, Cancel Delivery), поэтому отзыв доступа виден следующей проверке сразу. Проверки `platform:nomnom` и ресурсов без revision — `minimize_latency`.

## Relationship lifecycle

Business storage bounded context остаётся source of truth; SpiceDB — производный authorization graph. Для каждой relation schema review обязан заполнить lifecycle contract:

| Поле | Требование |
| --- | --- |
| Definition owner | Bounded context ресурса. |
| Source of truth | Конкретное business state/association. |
| Create trigger | Command или integration event и writer. |
| Delete trigger | Удаление, архивирование, отзыв или замена association. |
| Rollback/compensation | Что происходит с relationship при неуспехе или компенсации. |
| Consistency window | Синхронно либо документированная eventual consistency. |

Предварительный lifecycle для направляющей модели:

| Relation | Source of truth / writer | Create/delete и отказ |
| --- | --- | --- |
| `restaurant#owner@user` | Restaurant ownership в `restaurants`; writer `restaurants`. | Creation-time `TOUCH` до local commit; удаление/архивация удаляет relationships по resource. Failed write aborts commit; failed commit leaves orphan for reconciliation. |
| `order#customer@user`, `order#restaurant@restaurant` | Создаваемый Order в `orders`; writer `orders`. | Creation-time associations до commit, если нужны для немедленного доступа. Compensation/removal follows Order lifecycle; orphan безопасен до reconciliation. |
| `restaurant#staff@user` | `Restaurant.staff` в `restaurants`; writer — use cases Assign Staff / Revoke Staff. | Синхронный `TOUCH`/`DELETE` до сохранения; расхождение исправляет reconciliation `restaurants`. |
| `delivery#courier@user` | `Delivery.courier_id` в `delivery`; writer — use cases Accept Delivery Offer, Reassign Courier, Cancel Delivery. | Синхронный `TOUCH`/`DELETE` до commit (change `add-restaurant-delivery-relationships`); расхождение исправляет reconciliation `delivery`. |
| `delivery#order@order` | Delivery association в `delivery`; writer `delivery`. | Стратегия определяется use case: sync at creation when required immediately, otherwise Outbox. |

Таблица не фиксирует окончательную schema: names и exact triggers подтверждаются SDD use cases.

### Dual write rules

SpiceDB и local storage не образуют distributed transaction.

Creation-time/owner relationship:

```text
SpiceDB TOUCH → local DB commit
```

- SpiceDB write failed: local transaction rolls back; operation fails closed.
- SpiceDB write succeeded, local commit failed: graph contains orphan relationship. Resource lookup и domain operation всё равно не находят resource, поэтому доступ к business state не возникает. Reconciliation deletes orphan.

Derived relationship:

```text
business transaction + Outbox commit
              ↓
Kafka → dedicated consumer + Inbox → SpiceDB TOUCH/DELETE
```

Consumer side effect в SpiceDB находится вне Inbox transaction, поэтому write должен быть идемпотентным и retryable. `TOUCH` заменяет `CREATE`; повторный `DELETE` при отсутствии relation успешен. Inbox фиксирует completion только после успешного side effect либо применяется эквивалентная recoverable state machine, чтобы crash не потерял update.

При удалении/архивировании resource выполняется delete relationships по resource filter. Reconciliation периодически сравнивает business state и graph, исправляет missing/stale/orphan relationships и сообщает drift metric. Он не заменяет основной write path. Для `order#customer` он реализован в `orders` (`cmd/orders-reconciler`), для `restaurant#owner/staff`, `courier#self` и `delivery#courier` — фоновым циклом процессов `restaurants` и `delivery` (до постоянного хранилища): orphan/stale relationship удаляется только после двух проходов с интервалом не меньше grace, потому что creation-time `TOUCH` предшествует commit; missing восстанавливается сразу с обновлением authorization revision.

## Network and runtime

SpiceDB — инфраструктурный endpoint. Вызов не идёт через business internal gateway:

```text
bounded context ── infrastructure egress ──► SpiceDB
```

NetworkPolicy добавляет SpiceDB в `allow-infrastructure-egress` только для workloads с authorization adapter. Доступ к business API по-прежнему идёт через internal gateway. Network isolation ограничивает caller, но не доказывает subject identity.

Endpoint, credentials и TLS передаются через runtime configuration. Credentials не записываются в image, `values.yaml` или Git. SpiceDB разворачивается отдельно со своим datastore; datastore и HA — отдельное решение.

Liveness не зависит от SpiceDB. Readiness не делает синхронный dependency check и по умолчанию остаётся успешной при краткой деградации, если process может fail closed и не истощает ресурсы. При реализации конкретного process документируется, способен ли он выполнять хоть одну функцию без SpiceDB.

## Failure policy

Для state-changing use cases:

- `DENIED` → `PERMISSION_DENIED`, без domain mutation;
- invalid authorization request/schema mismatch → fail closed и integration error;
- `ERROR`/`UNAVAILABLE` → fail closed;
- `TIMEOUT` → fail closed;
- каждый call имеет bounded timeout и общий retry budget в пределах request deadline;
- circuit breaker после порога ошибок быстро отклоняет защищённые операции и не допускает exhaustion thread/connection pools.

Read-only UI может получить иной fallback только после отдельного решения, которое описывает риск раскрытия данных и допустимую stale behavior.

## Caching

Application-level cache permission results по умолчанию отсутствует. Перед его добавлением нужны:

- измеримая latency/load problem;
- TTL и invalidation;
- consistency requirement;
- максимально допустимое окно устаревшего разрешения или запрета;
- поведение при revoke и outage.

## Observability

OpenTelemetry покрывает:

- check latency и result: `allowed`, `denied`, `error`, `timeout`;
- relationship write latency/failures;
- circuit breaker state/rejections;
- reconciliation scanned, repaired and drift count.

Metric labels: `permission`, `resource_type`, result и низкокардинальный error class. `user_id`, `service_id`, `order_id`, `resource_id` и ZedToken не являются metric labels. Resource identifiers в traces/logs допускаются только по общей privacy/cardinality policy.

Logs различают:

- ожидаемый denied;
- dependency unavailable/timeout;
- invalid authorization request;
- schema/integration error.

## Testing contract

Unit tests use case используют test implementation authorization port, а не mock SpiceDB SDK. Integration tests используют настоящий SpiceDB через Testcontainers.

Обязательные сценарии:

1. permission granted;
2. permission denied;
3. relationship added;
4. relationship removed;
5. immediate check after write with ZedToken;
6. SpiceDB unavailable → state-changing operation fails closed;
7. repeated relationship update is idempotent;
8. unauthorized request does not change domain state;
9. SpiceDB write succeeds, DB commit fails: protected operation cannot access absent resource, reconciliation removes orphan;
10. `service` subject executes an authorized Temporal Activity or consumer use case without user impersonation.

Kubernetes/NetworkPolicy and E2E scenarios run in kind.

## Открытые вопросы

- definitions, relations and permissions after domain SDD use cases are agreed;
- propagated access token or signed internal identity for the user subject via BFF (service identity is decided by ADR 27);
- exact local field, serialization and gRPC metadata key for ZedToken;
- SpiceDB datastore, deployment topology, backups and HA;
- reconciliation ownership, schedule, batching, repair policy and drift SLO;
- concrete timeout/retry/circuit-breaker values and read-only degradation policies;
- precise lifecycle contract for every finalized relationship.

## References

- [Authzed documentation](https://authzed.com/docs)
- [Schema language reference](https://authzed.com/docs/spicedb/concepts/schema)
- [Relationships](https://authzed.com/docs/spicedb/concepts/relationships)
