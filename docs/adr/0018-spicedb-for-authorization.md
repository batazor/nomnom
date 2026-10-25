# 18. SpiceDB for Authorization

Date: 2026-10-24

## Status

Accepted

Связан с [6. DDD Structure inside Vertical Slices](0006-ddd-structure-inside-vertical-slices.md)

Связан с [11. Transactional Inbox and Idempotent Event Processing](0011-transactional-inbox-and-idempotent-event-processing.md) и [13. Transactional Outbox for Integration Event Publishing](0013-transactional-outbox-for-integration-event-publishing.md)

Связан с [15. Использовать internal gateway для синхронных вызовов бизнес-API](0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md), [16. Запрещать обход internal gateway через Cilium и NetworkPolicy](0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md) и [17. Cloud Native Application Runtime](0017-cloud-native-application-runtime.md)

## Context

BiteRoute должен отвечать не только на вопрос, кто выполняет запрос, но и на вопрос, может ли этот subject выполнить конкретное действие над конкретным resource. Проверки ролей, принадлежности и связей, размноженные между BFF и bounded contexts, быстро расходятся и заставляют переносить роли через бизнес-код. При этом BFF не владеет доменными данными, а окончательное решение о выполнении операции остаётся у bounded context — владельца use case.

Authentication и authorization являются разными ответственностями. Authentication устанавливает identity вызывающей стороны. Authorization решает, разрешено ли этой identity действие над resource. Identity provider, login, sessions и выпуск или проверка access token не должны становиться ответственностью authorization engine.

Отдельно требуется согласовать централизованный authorization graph с локальными транзакциями bounded contexts. SpiceDB и business storage не участвуют в одной атомарной транзакции. Проверки после изменения relationship требуют явной consistency, consumers и Temporal Activities выполняются без интерактивного пользователя, а недоступность общей authorization-системы не должна приводить к несанкционированному изменению состояния.

На этом шаге фиксируется архитектурное решение. `.zed` schema, SDK-интеграция, deployment SpiceDB, datastore, credentials, NetworkPolicy manifests и реализация reconciliation не создаются.

## Decision

### Централизованный ReBAC

Использовать [SpiceDB](https://authzed.com/docs) как централизованную систему authorization. Модель — relationship-based access control:

```text
subject → relationship → resource → permission
```

Приложение задаёт вопрос вида `Can user:123 cancel order:456?` через `CheckPermission`. Relations именуются существительными (`owner`, `staff`, `customer`), permissions — глаголами из согласованных use cases (`view`, `cancel`, `accept`). Роли, которые выражаются relations и permissions, не распространяются вручную через бизнес-код.

SpiceDB хранит authorization graph. Бизнес-данные и source of truth о ресторанах, заказах, доставках и назначениях остаются в storage соответствующих bounded contexts. SpiceDB не является identity provider и не отвечает за login, sessions или токены.

### Subject context

Поддерживаются два типа subject:

- `user` — аутентифицированный конечный пользователь;
- `service` — workload identity BFF, API, Temporal Activity worker, Kafka consumer или фоновой задачи. Понятие system operation представляется конкретной `service` identity, а не общим привилегированным subject.

BFF проверяет внешнюю identity, но bounded context не доверяет клиентскому или произвольному внутреннему header. Subject передаётся через проверяемый владельцем операции механизм: исходный access token либо подписанную internal identity. Конкретный механизм выбирается отдельным решением. NetworkPolicy, internal gateway и mTLS могут ограничивать транспорт, но не заменяют криптографическую проверку subject context.

Temporal Activities, event consumers и фоновые задачи используют собственную `service` identity с минимальными permissions и не impersonate пользователя. Если асинхронная операция была инициирована пользователем, исходный subject сохраняется в данных операции для audit/correlation, но разрешение пользователя проверяется при принятии исходной команды и не переигрывается из workflow или consumer.

### Место проверки

BFF выполняет дополнительные coarse-grained проверки: доступ к endpoint, раннее отклонение и фильтрацию клиентского сценария. Эти проверки не являются достаточными для изменения состояния.

Каждый bounded context выполняет authoritative authorization check непосредственно перед защищённым application use case, в том числе для вызова от BFF:

```text
client → BFF → orders
                   └→ CheckPermission(user:123, cancel, order:456)
                   └→ domain operation
```

Authorization отвечает, имеет ли subject право попытаться выполнить действие. Затем доменная модель отдельно проверяет текущее состояние и инварианты, например что доставленный заказ уже нельзя отменить. Порядок для защищённой операции: сначала authorization, затем domain rules. Отказ authorization не изменяет domain state.

### Размещение в VSA

Authorization port располагается в `applications/` рядом с использующим его use case. Он выражает необходимую capability (`check`, а для list-сценария — выбранную lookup/bulk capability), а не повторяет весь SpiceDB API. SpiceDB client, преобразование типов, consistency, timeouts, retries, circuit breaker и observability находятся в adapter в `infrastructure/`. `domains/` и `domains/<domain>/vo/` не зависят от SpiceDB SDK, gRPC, ZedToken или `.zed` schema.

### Shared schema contract

В системе будет одна schema по пути `authorization/schema.zed`. Каждый resource definition имеет owner — bounded context, владеющий этим resource. Initial direction для последующего моделирования: `user`, `service`, `restaurant`, `order`, `delivery`; relations `owner`, `staff`, `customer`, `restaurant`, `courier`, `order`. Это не фиксирует definitions до анализа SDD use cases.

Schema изменяется через review. CI выполняет `zed validate` и assertions с granted/denied cases. Удаление или изменение relation/permission считается breaking change и проходит управляемую миграцию, аналогичную эволюции Protobuf-контрактов: сначала добавление и dual-compatible rollout, затем перенос relationships и callers, и только потом удаление. Сам `.zed`-файл этим ADR не создаётся.

### Lists

`CheckPermission` не используется как фильтр произвольной коллекции. Для каждого list use case выбирается один из вариантов:

- domain query фильтрует по известному владельцу, например `customer_id = subject`, не дублируя знание business storage в SpiceDB;
- `LookupResources` или `LookupSubjects` используется для cross-cutting отношений, например staff ресторана или courier;
- `CheckBulkPermissions` пост-фильтрует небольшую bounded выборку.

Выбор, лимиты и pagination фиксируются при проектировании use case.

### Consistency и ZedToken

- Обычная проверка использует `minimize_latency`.
- Проверка, которая должна увидеть собственную запись relationship, использует `at_least_as_fresh` с ZedToken из ответа write.
- `fully_consistent` допускается только по документированной причине.

Для `create → immediately check` owner/creation relationship записывается до commit локальной транзакции. Полученный ZedToken сохраняется как техническая authorization revision рядом с resource в той же локальной транзакции. Если немедленная проверка происходит в следующем внутреннем вызове, token также передаётся в gRPC metadata; internal gateway обязан сохранить эту metadata. Точный формат поля и metadata key определяются вместе с интеграцией.

### Relationship lifecycle и dual write

Запись SpiceDB и commit business storage не являются атомарными. Применяются три механизма:

1. Creation-time и owner relationships, необходимые до публикации resource, записываются в SpiceDB синхронно до commit локальной транзакции. Ошибка write отменяет транзакцию — fail closed. Если local commit падает после успешного write, остаётся orphan relationship к несуществующему resource. Protected use case всё равно не находит resource, а reconciliation удаляет orphan.
2. Производные relationships, для которых допустима задержка (например, staff назначен или courier принял delivery), доставляются через Transactional Outbox в dedicated consumer с Transactional Inbox. Допустимое окно eventual consistency документируется в use case.
3. Периодический reconciliation сравнивает business source of truth со SpiceDB, исправляет drift и публикует метрику. Это страховка, а не основной путь записи.

Каждый relationship в будущей schema получает lifecycle contract: owner definition; business source of truth; кто и по какому command/event создаёт relationship; кто и когда удаляет; поведение при rollback/compensation; допустимая consistency lag. Writes идемпотентны: upsert использует `TOUCH`, удаление принимает отсутствие как успешный результат. При удалении или архивации resource его relationships удаляются по resource filter.

### Network и runtime

SpiceDB — инфраструктурная система, а не business API. Приложения обращаются к нему напрямую по инфраструктурной сетевой политике и не маршрутизируют вызовы через internal gateway. `allow-infrastructure-egress` разрешает endpoint SpiceDB только workloads, которым он нужен.

Endpoint, credentials и TLS поступают через runtime configuration. Credentials не хранятся в image, `values.yaml` или Git. SpiceDB имеет собственный datastore; его выбор, deployment и HA требуют отдельного инфраструктурного решения.

Кратковременная недоступность SpiceDB не делает liveness неуспешной. Readiness по умолчанию также не вызывает SpiceDB синхронно и не падает, если процесс способен быстро fail closed; окончательная readiness policy определяется при реализации процесса.

### Failures, caching и observability

State-changing операции fail closed при `DENIED`, `ERROR` или `TIMEOUT`. Каждый check получает bounded timeout и ограниченный retry budget. Circuit breaker переводит зависимость в быстрый отказ, чтобы защищённые вызовы не исчерпали ресурсы процесса, а независимые возможности сервиса продолжали работать. Альтернативное поведение read-only UI требует отдельного решения для конкретного use case.

Application-level cache authorization-решений по умолчанию не вводится. Любой будущий cache требует измеримой причины, TTL, invalidation, consistency requirements и явно допустимого окна stale access.

OpenTelemetry различает latency, allowed, denied, errors, timeouts, relationship write failures и reconciliation drift. Metric labels ограничены `permission`, `resource_type`, результатом и низкокардинальным классом ошибки; `user_id`, `order_id` и другие resource identifiers не являются labels. Идентификаторы в traces подчиняются privacy/cardinality policy. Logs различают denied, unavailable, invalid request и schema/integration error.

### Testing

Unit tests используют test implementation authorization port. Integration tests запускают настоящий SpiceDB через Testcontainers. Обязательны granted, denied, add/remove relationship, immediate check с ZedToken, unavailable → fail closed, идемпотентный repeat update, отсутствие domain mutation после denial, orphan после failed DB commit и его reconciliation, а также `service` subject для Temporal/consumer. Kubernetes/E2E выполняются в kind.

Подробный contract и открытые вопросы описаны в [документации security](../security.md).

## Consequences

Authorization model, schema и relationship graph становятся общесистемным shared contract, но решение о выполнении операции остаётся локальным у bounded context. BFF может раньше отклонять запросы, не превращаясь в единственную enforcement point. Доменная модель не получает зависимость от authorization engine.

SpiceDB становится общей runtime-зависимостью защищённых операций. Нужны отдельные deployment/datastore/HA решения, schema governance, subject verification, reconciliation и эксплуатационные SLO. Синхронная запись creation relationship увеличивает latency создания и оставляет безопасные orphan relationships при редком отказе local commit; асинхронные derived relationships получают документированное окно eventual consistency.

Fail-closed защищает state changes, но во время деградации SpiceDB часть операций будет недоступна. Bounded timeouts, retry budgets и circuit breaker ограничивают blast radius. ZedToken делает read-after-write явным, но требует переноса технической revision через storage или metadata.

## References

- [Authzed documentation](https://authzed.com/docs)
- [SpiceDB schema language](https://authzed.com/docs/spicedb/concepts/schema)
- [SpiceDB relationships](https://authzed.com/docs/spicedb/concepts/relationships)
