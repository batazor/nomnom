# Security: authentication and authorization

Документ определяет общесистемный contract authentication context и authorization в nomnom. Архитектурное решение зафиксировано в [ADR 18](adr/0018-spicedb-for-authorization.md). Schema, grants развёртывания и assertions лежат в [`authorization/`](../authorization/schema.zed); SpiceDB-адаптеры — в `infrastructure/` bounded contexts; SpiceDB тестового окружения разворачивается в kind (`ops/kind/bootstrap/templates/authz.yaml`, memory datastore). Production datastore, deployment topology и HA не выбраны.

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

Обычный header `x-user-id`, `x-role`, `x-tenant` или аналогичное значение не является доказательством identity. Internal gateway обязан сохранить authorization metadata, но факт прохождения через gateway и NetworkPolicy не делает её доверенной.

### User identity

Пользовательский subject передаётся вторым механизмом — BFF-signed user assertion ([ADR 32](adr/0032-user-identity-via-bff-signed-assertions.md)). BFF проверяет access token покупателя и на каждый внутренний вызов подписывает ES256 JWT (`iss: nomnom-bff`, `aud` целевого контекста, `sub` пользователя, срок жизни не больше 60 s, `jti`) в metadata `x-nomnom-user-assertion`. Контекст принимает assertion только вместе с service identity BFF по ADR 27 и только от service из allowlist `IDENTITY__USER_DELEGATES`; подпись проверяется по JWKS BFF (`IDENTITY__USER_JWKS_FILE`), `iss` — по `IDENTITY__USER_ISSUER`. Результат — `user:<sub>`; без этих параметров user assertions отклоняются (fail closed).

### Service identity

`service` subject между bounded contexts доказывается Kubernetes projected ServiceAccount token ([ADR 27](adr/0027-service-identity-via-projected-serviceaccount-tokens.md)): metadata `authorization: Bearer <jwt>`, audience целевого контекста (`nomnom.orders`, `nomnom.restaurants`, `nomnom.delivery`, `nomnom.payments`, `nomnom.controlplane`), подпись по JWKS кластера, `iss`, `exp`/`nbf` с допуском 30 s и allowlist `namespace/serviceaccount=service-name`. Любое нарушение — `UNAUTHENTICATED`; без `IDENTITY__ISSUER`, `IDENTITY__AUDIENCE` и `IDENTITY__JWKS_FILE` сервер отклоняет всех (fail closed). Resolver только аутентифицирует: permissions identity по-прежнему проверяет SpiceDB в use case. Правила проверки во всех языках закреплены общими векторами `tests/identity`.

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

Definitions и relations [`authorization/schema.zed`](../authorization/schema.zed):

```text
user, service
platform: fulfillment (service), operator (user), dispatcher (user)
order: customer, restaurant, delivery
customer: self
restaurant: owner, staff
courier: self
delivery: courier
```

Новые definitions и permissions добавляются по SDD use cases, которым они нужны.

## Schema как shared contract

Единая schema системы хранится в [`authorization/schema.zed`](../authorization/schema.zed), grants развёртывания — в [`authorization/bootstrap.yaml`](../authorization/bootstrap.yaml), assertions путей пользовательских прав — в [`authorization/validation.yaml`](../authorization/validation.yaml) (`make authz-validate`, локальный SpiceDB — `make spicedb-dev`). Команды Order Fulfillment Workflow — capabilities синглтона `platform:nomnom`, выданные relation `fulfillment@service:orders-worker`: worker создаёт и адресует ресурсы по `order_id` раньше, чем у них появляются relationships. Пользовательские права остаются resource-level. Соответствие портов контекстов и permissions — в [design change `add-spicedb-authorization`](../openspec/changes/archive/2026-10-25-add-spicedb-authorization/design.md#соответствие-портов-и-permissions-нормативно).

Каждый resource definition имеет owner:

| Resource | Owner |
| --- | --- |
| `restaurant` | `restaurants` |
| `order` | `orders` |
| `courier`, `delivery` | `delivery` |
| `customer` | `orders`; permission `view_orders` защищает историю заказов Customer, id совпадает с id `user` |
| `platform` | Shared contract; grants `service` identities — `authorization/bootstrap.yaml`, `operator`/`dispatcher` — bootstrap окружения (`ops/kind/authz/bootstrap-kind.yaml`). |
| `user`, `service` | Общесистемный identity contract; lifecycle определяется вместе с authentication/workload identity. |

Порты authorization контекстов (SpiceDB-адаптеры переводят их в definitions `schema.zed`):

| Boundary | Где объявлены | Что используется |
| --- | --- | --- |
| `orders` | Константы use-case packages и `AccessCheck` в `pkg/authz` | `view` и `cancel` на `order:<id>`, `view_orders` на `customer:<id>` для чужой истории заказов |
| `restaurants` | Ports `RestaurantAccessControl`, `MenuAccessControl`, `KitchenAccessControl` в `applications/access-control.ts` слайсов | Методы выражают нужду use case (`canManageRestaurant`, `canManageMenu`, `canOperateKitchen`, …), а не имя permission |
| `delivery` | Ports `CourierAuthorization`, `DeliveryAuthorization` с enum permissions по use cases | Resources `Platform`, `Courier(id)`, `Delivery(id)`. Команды курьера — `courier#act`; принадлежность доставки проверяет домен. Действие диспетчера (Report Delivery Failure на `Delivery(id)`) — `platform:nomnom#dispatch_delivery` (`operator`), а не `delivery#act_as_courier`, которое есть только у назначенного курьера. Offer доставки заказа выбранному курьеру (Offer Delivery To Courier на `Platform`) — `platform:nomnom#offer_delivery` (`operator` и `dispatcher` Courier Pool). |
| `payments` | `PaymentPermission` и `PaymentPermissionChecker` в `slices/payment/applications/ports.py` | Permissions из строки `payment` выше над `PaymentResource(payment_id)` |

Definition `payment` нет: fulfillment-команды адресуют платёж по `order_id` раньше любых resource-level связей, поэтому `create`, `authorize`, `capture`, `cancel_authorization`, `refund` проверяются как `platform:nomnom#manage_payments`, `view` и `view_operations` — как `platform:nomnom#view_payments` (`boundaries/payments/infrastructure/authorization/spicedb.py`). Provider callback и operation result в `payments` не проходят SpiceDB-проверку: actor не является subject authorization graph, подлинность callback проверяет infrastructure (service-path; см. [design.md change `implement-payments-application-layer`](../openspec/changes/archive/2026-10-25-implement-payments-application-layer/design.md)). Решения по каждому контексту — в design.md changes `implement-{orders,restaurants,delivery,payments}-application-layer`.

Изменения schema проходят review с участием владельцев затронутых definitions и consumers permissions. Проверки:

- `make authz-validate` — `zed validate` для `bootstrap.yaml` (grants развёртывания) и `validation.yaml` (granted и denied paths пользовательских прав); в CI пока не запускается;
- integration-тесты контекстов против SpiceDB с `authorization/bootstrap.yaml` — regression cases для permissions, используемых приложениями.

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

Реализация: `orders` хранит в строке `Order` revision последней записи его relationships (`order#customer`/`restaurant` при оформлении, `order#delivery` при назначении курьера); `restaurants` — revision `restaurant#owner/staff` в `Restaurant`; `delivery` — revision `courier#self` в `Courier` и `delivery#courier` в `Delivery`. Revision сохраняется и после `DELETE` (Revoke Staff, Reassign Courier, Cancel Delivery, Confirm Delivery, Report Delivery Failure, смена или отмена Delivery в `orders`), поэтому отзыв доступа виден следующей проверке сразу. Проверки `platform:nomnom` и ресурсов без revision — `minimize_latency`.

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

Lifecycle relations schema:

| Relation | Source of truth / writer | Create/delete и отказ |
| --- | --- | --- |
| `restaurant#owner@user` | Restaurant ownership в `restaurants`; writer `restaurants`. | Creation-time `TOUCH` до local commit; удаление/архивация удаляет relationships по resource. Failed write aborts commit; failed commit leaves orphan for reconciliation. |
| `order#customer@user`, `order#restaurant@restaurant` | `Order.customer_id`, `Order.restaurant_id` в `orders`; writer — Sync Order Relationships в `orders-consumer` по `OrderRelationshipsChanged`, которое Place Order кладёт в Outbox своей транзакции. | Синхронизация по состоянию (ADR 18): связи приводятся к строке заказа одной записью, revision сохраняется в заказе. Откат placement откатывает и событие — orphan нет. До синхронизации Customer читает и отменяет свой заказ по `customer_id` без SpiceDB. |
| `order#delivery@delivery` | `Order.delivery_id` в `orders` из `delivery.v1` `CourierAssignedToDelivery` и `DeliveryCancelled`; Record Courier Assignment (новая Delivery) и Record Courier Release (`DeliveryCancelled`) кладут `OrderRelationshipsChanged` в Outbox своей Inbox-транзакции, writer — Sync Order Relationships. | Синхронизация по состоянию: `delivery_id` строки становится единственной `order#delivery`, `NULL` — связи нет; переназначение в пределах той же Delivery событие не пишет. Событие с Delivery `aggregate_version` ниже сохранённого назначения или снятия пропускается (FR-564). Relation `order#courier` нет: кто курьер сейчас, решает только `delivery` через `delivery#courier`, а `order#view` курьера идёт стрелкой `delivery->act_as_courier`. |
| `restaurant#staff@user` | `Restaurant.staff` в `restaurants`; writer — use cases Assign Staff / Revoke Staff. | Синхронный `TOUCH`/`DELETE` до сохранения; расхождение исправляет reconciliation `restaurants` (missing — после двух проходов с интервалом не меньше grace, чтобы не вернуть доступ по документу, прочитанному до сохранения Revoke Staff). |
| `delivery#courier@user` | Активное назначение в `delivery`: `Delivery.courier_id` у доставки в состоянии assigned или picked up (`Delivery::active_courier`, FR-444). `courier_id` доставленной или неуспешной доставки остаётся фактом «кто вёз» и доступа не даёт. Writers — Accept Delivery Offer (`TOUCH`); Reassign Courier, Cancel Delivery, Confirm Delivery, Report Delivery Failure (`DELETE`). | Синхронный `TOUCH`/`DELETE` до commit с сохранением revision; неудачный `DELETE` откатывает use case. Reconciliation `delivery` строит ожидаемые связи из активных назначений: связь закрытой доставки удаляется как stale и не восстанавливается (FR-563). |

`courier#self@user` — `TOUCH` в Register Courier (`delivery`), reconciliation — как у `delivery#courier`. `customer#self@user` в runtime не пишется: свою историю Customer читает по `customer_id` без graph, другому subject `view_orders` не выдаётся (связь есть только в `validation.yaml`). `platform#*` задаёт bootstrap развёртывания.

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

State sync (`orders`, ADR 18):

```text
business transaction + Outbox OrderRelationshipsChanged commit
              ↓
orders.v1.order-relationships → orders-consumer
  → FOR SHARE строки → ReadRelationships → один WriteRelationships (TOUCH нужных, DELETE остальных)
  → authorization_revision
```

Синхронизация читает состояние, а не событие: Inbox не нужен, повтор и redrive безопасны, orphan не возникает. После потери данных SpiceDB — `orders-admin authz resync`.

При удалении/архивировании resource выполняется delete relationships по resource filter. Reconciliation периодически сравнивает business state и graph, исправляет missing/stale/orphan relationships и сообщает drift metric. Он не заменяет основной write path. В `orders` его нет: связи `order` пишет только state sync. Для `restaurant#owner/staff`, `courier#self` и `delivery#courier` он реализован фоновым циклом процессов `restaurants` и `delivery` (до постоянного хранилища): orphan/stale relationship удаляется только после двух проходов с интервалом не меньше grace, потому что creation-time `TOUCH` предшествует commit. Writers также делают `DELETE` до commit, поэтому missing relationship нельзя восстанавливать по business state, прочитанному до commit такого writer'а: иначе снятый курьер получил бы доступ обратно до истечения orphan grace. В `delivery` writers пишут SpiceDB до `UPDATE` строки и lock не держат, поэтому missing `courier#self`/`delivery#courier` восстанавливается, как и orphan удаляется, только если была missing с тем же subject и той же версией строки `Courier`/`Delivery` на двух проходах с интервалом не меньше grace: другая версия означает commit writer'а между проходами (курьера сняли, снова назначили и снова сняли), и grace начинается заново (FR-563, AC-760). В `restaurants` Revoke Staff пишет `DELETE restaurant#staff` до сохранения документа в MongoDB, а MongoDB не даёт lock, который reconciler мог бы дождаться, поэтому missing `restaurant#owner`/`restaurant#staff` восстанавливается по тому же двухпроходному правилу с grace (счётчик `missing_pending`); grace должен превышать длительность writer'а. Восстановление обновляет authorization revision.

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

- production identity provider (kind uses Dex with authorization code + PKCE, ADR 35; user subject propagation is decided by ADR 32);
- exact local field, serialization and gRPC metadata key for ZedToken;
- SpiceDB datastore, deployment topology, backups and HA;
- reconciliation ownership, schedule, batching, repair policy and drift SLO;
- concrete timeout/retry/circuit-breaker values and read-only degradation policies;

## References

- [Authzed documentation](https://authzed.com/docs)
- [Schema language reference](https://authzed.com/docs/spicedb/concepts/schema)
- [Relationships](https://authzed.com/docs/spicedb/concepts/relationships)
