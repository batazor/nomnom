# 27. Service identity via projected ServiceAccount tokens

Date: 2026-10-25

## Status

Accepted

Уточняет [18. SpiceDB for Authorization](0018-spicedb-for-authorization.md)

Уточнён [32. User identity via BFF-signed assertions](0032-user-identity-via-bff-signed-assertions.md)

Связан с [15. Использовать internal gateway для синхронных вызовов бизнес-API](0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md), [16. Запрещать обход internal gateway](0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md) и [17. Cloud Native Application Runtime](0017-cloud-native-application-runtime.md)

## Context

[ADR 18](0018-spicedb-for-authorization.md) и [security](../security.md#subject-context) требуют, чтобы bounded context получал subject только через проверяемый механизм, и оставляют открытым выбор между propagated token и signed internal identity. Все gRPC-серверы сейчас используют `RejectingSubjectResolver`, поэтому синхронные вызовы между контекстами, например Activities Order Fulfillment Workflow, отклоняются `UNAUTHENTICATED`. Для workload-to-workload вызовов нет пользователя, чей token можно передать: нужна собственная проверяемая `service` identity.

## Decision

### Service-to-service: projected ServiceAccount token

Workload доказывает свою `service` identity Kubernetes projected ServiceAccount token — JWT, который подписывает кластер, kubelet ротирует, а Pod получает файлом. Отдельный issuer, хранение приватных ключей и их ротация приложениям не нужны.

- **Audience на целевой контекст:** `nomnom.orders`, `nomnom.restaurants`, `nomnom.delivery`, `nomnom.payments`. Pod вызывающего получает по отдельному token на каждый target (projected volume), поэтому token одного контекста нельзя предъявить другому.
- **Транспорт:** gRPC metadata `authorization: Bearer <jwt>`. Internal gateway пропускает metadata без изменения и не участвует в проверке.
- **Проверка на сервере:** подпись RS256/ES256 по JWKS кластера (`kid` обязателен, `alg: none` и HMAC отклоняются), `iss` равен настроенному issuer, `aud` содержит audience контекста, `exp`/`nbf` с допуском 30 s.
- **Subject:** `sub` вида `system:serviceaccount:<namespace>:<name>` переводится в `Subject{service, <service-name>}` только по явному allowlist `namespace/serviceaccount=service-name`. Неизвестный ServiceAccount, как и любой невалидный token, — `UNAUTHENTICATED`. Что разрешено этой identity, решает SpiceDB в use case, не resolver.
- **JWKS:** публичные ключи кластера (`/openid/v1/jwks`) передаются приложению файлом из ConfigMap, который создаёт развёртывание; приложение не обращается к Kubernetes API и не зависит от него при проверке. Невалидный JWKS — ошибка startup, в том числе любой ключ, который нельзя использовать для проверки: без `kid`, с повторяющимся `kid`, с `use` не `sig`, не RSA и не EC P-256 или с `alg`, не совпадающим с типом ключа. Неподходящие ключи не пропускаются молча.
- **Fail fast:** без issuer, audience, JWKS или allowlist процесс с gRPC API не стартует; режима с отключённой проверкой нет.

Конфигурация одинакова во всех контекстах: `IDENTITY__ISSUER`, `IDENTITY__AUDIENCE`, `IDENTITY__JWKS_FILE`, `IDENTITY__SERVICE_ACCOUNTS`. Вызывающий читает token из `IDENTITY__TOKEN_DIR/<audience>` при каждом вызове, потому что kubelet обновляет файл.

### User identity

Пользовательский subject через BFF этим ADR не решается: выбор между propagated access token и BFF-signed internal identity остаётся за проектированием BFF identity flow. Resolver контекста расширяется вторым verifier'ом, не меняя service-проверку. Решение — [ADR 32. User identity via BFF-signed assertions](0032-user-identity-via-bff-signed-assertions.md).

## Consequences

Activities, consumers и jobs получают проверяемую `service` identity без собственного PKI; ротация ключей подписи и token выполняется кластером. Каждый контекст проверяет одинаковые правила, совместимость реализаций на Go, Python, TypeScript и Rust закреплена общими тестовыми векторами `tests/identity`.

Identity привязана к Kubernetes: вне кластера (локальный запуск) токен и JWKS задаются тестовыми файлами. Ротация signing key кластера требует обновить ConfigMap с JWKS; до обновления новые tokens отклоняются. Replay token в пределах его срока жизни внутри одной audience не предотвращается — это компенсируется коротким сроком жизни projected token, NetworkPolicy и authorization в SpiceDB.
