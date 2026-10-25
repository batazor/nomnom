# 32. User identity via BFF-signed assertions

Date: 2026-10-25

## Status

Accepted

Уточняет [27. Service identity via projected ServiceAccount tokens](0027-service-identity-via-projected-serviceaccount-tokens.md) и [18. SpiceDB for Authorization](0018-spicedb-for-authorization.md)

Связан с [BFF ADR 2. BFF как клиентская точка входа](../../boundaries/bff/docs/adr/0002-bff-as-the-client-entry-point.md)

## Context

[ADR 27](0027-service-identity-via-projected-serviceaccount-tokens.md) решил `service` identity между контекстами, но оставил открытым, как bounded context получает `user` subject от BFF. [Security](../security.md#subject-context) допускает два механизма: передать исходный access token покупателя или выпустить в BFF короткоживущую подписанную internal identity. Пока выбора нет, resolvers customer GraphQL API не могут вызвать `orders` от имени покупателя: вызов от `service:bff` обходит authoritative check «покупатель видит только свой заказ».

Ограничения:

- [BFF ADR 2](../../boundaries/bff/docs/adr/0002-bff-as-the-client-entry-point.md): BFF проверяет access token OIDC-провайдера, а контекст доверяет пользовательскому контексту только от аутентифицированной и явно авторизованной identity BFF.
- Контексты написаны на Go, Python, TypeScript и Rust; правила проверки должны совпадать, как для service identity (`tests/identity`).
- Access token покупателя выпускается для audience BFF. Контекст, принимающий такой token, принимает token чужой audience, и любой его держатель может обратиться к любому контексту.
- Identity provider ещё не выбран; контексты не должны зависеть от его формата claims и ротации ключей.

## Decision

### BFF выпускает user assertion на каждый вызов контекста

BFF проверяет access token покупателя (подпись по JWKS identity provider, `iss`, `aud` BFF, срок, не ID token) и для каждого внутреннего вызова подписывает **user assertion** — JWT с claims:

| Claim | Значение |
| --- | --- |
| `iss` | `nomnom-bff` |
| `aud` | audience целевого контекста: `nomnom.orders`, `nomnom.restaurants`, `nomnom.delivery`, `nomnom.payments` |
| `sub` | идентификатор пользователя — `sub` проверенного access token |
| `iat`, `nbf`, `exp` | `exp - iat` не больше 60 s |
| `jti` | случайный идентификатор для корреляции и audit |

Алгоритм — ES256, `kid` обязателен. Assertion передаётся в gRPC metadata `x-nomnom-user-assertion`, отдельно от `authorization`.

### Контекст принимает user только вместе с service identity BFF

Вызов от BFF несёт два доказательства:

1. `authorization: Bearer <projected ServiceAccount token>` — service identity BFF по ADR 27, без изменений;
2. `x-nomnom-user-assertion: <jwt>` — пользователь, от имени которого действует BFF.

Resolver контекста сначала аутентифицирует service identity. Если assertion присутствует, вызывающий service MUST входить в allowlist делегатов `IDENTITY__USER_DELEGATES` (по умолчанию пуст, в развёртываниях — `bff`); затем assertion проверяется: подпись по JWKS BFF (`IDENTITY__USER_JWKS_FILE`), `iss` равен `IDENTITY__USER_ISSUER`, `aud` содержит audience контекста (`IDENTITY__AUDIENCE`), `exp`/`nbf` с допуском 30 s, `exp - iat ≤ 60 s`, непустой `sub`. Результат — `Subject{user, <sub>}`; service identity BFF остаётся в контексте вызова для audit. Любое нарушение — `UNAUTHENTICATED`. Assertion от service вне allowlist — тоже `UNAUTHENTICATED`, даже если assertion валиден.

Без `IDENTITY__USER_ISSUER` и `IDENTITY__USER_JWKS_FILE` контекст не принимает user assertions (fail closed), service-вызовы работают как раньше.

Что разрешено пользователю, по-прежнему решает SpiceDB в use case по `user:<sub>`.

### Ключи

Приватный ключ подписи BFF хранится в Kubernetes Secret и монтируется файлом; публичный JWKS BFF доставляется контекстам ConfigMap, как JWKS кластера в ADR 27. Ротация: новый ключ сначала добавляется в JWKS контекстов, затем BFF переключается на него, старый удаляется после срока жизни assertion. Невалидный JWKS — ошибка startup.

### Внешняя аутентификация в BFF

Cosmo Router проверяет `Authorization: Bearer` access token (JWT authentication по JWKS identity provider) и отклоняет запрос до плагина. Router передаёт заголовок `Authorization` в плагин как gRPC metadata `authorization`; плагин проверяет token повторно и берёт `sub` только из проверенного token. Способ, которым браузер получает access token (OIDC authorization code + PKCE, httpOnly cookie или session BFF), и выбор identity provider решаются отдельно; это решение не зависит от них.

Совместимость правил на всех языках закрепляется общими векторами `tests/identity` для user assertions.

## Consequences

- Контексты не зависят от identity provider: формат claims, ротация его ключей и смена провайдера затрагивают только BFF.
- Assertion ограничен audience одного контекста и сроком 60 s, а без service token BFF бесполезен: украденный assertion нельзя предъявить другому контексту или вызвать им контекст из другого workload.
- BFF становится доверенным выпускающим центром: компрометация его ключа или процесса позволяет действовать от имени любого пользователя в течение жизни ключа. Это компенсируется Secret с минимальным доступом, ротацией ключа, NetworkPolicy и тем, что authorization каждого действия остаётся в SpiceDB.
- Replay assertion в пределах 60 s внутри одной audience не предотвращается; `jti` служит для audit, а не для дедупликации.
- Каждый контекст получает второй verifier и три параметра конфигурации; BFF — ключ подписи и минтер assertions.
- Если Router не сможет передавать `Authorization` в плагин, плагин получит проверенный Router `sub` через header rule с выражением по claims; граница router → plugin остаётся внутри одного Pod.
