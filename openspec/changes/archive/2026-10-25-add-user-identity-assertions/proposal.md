# Proposal

## Why

Resolvers customer GraphQL API должны вызывать `orders` и другие контексты от имени покупателя, но контексты принимают только `service` identity (ADR 27). Вызов от `service:bff` обходит authoritative check SpiceDB «покупатель видит и отменяет только свой заказ». [ADR 32](../../../docs/adr/0032-user-identity-via-bff-signed-assertions.md) выбрал механизм: BFF проверяет access token покупателя и на каждый внутренний вызов подписывает короткоживущий user assertion, а контекст принимает его только вместе с service identity BFF. Этот change реализует решение во всех контекстах и в BFF, не подключая ещё ни одного resolver.

## What Changes

- **Общие векторы.** `tests/identity` получает ключ BFF (ES256), JWKS и набор user assertions (валидный, истёкший, чужая audience, чужой issuer, срок жизни больше 60 s, пустой `sub`, чужая подпись, неизвестный `kid`, `alg: none`) и сценарии делегата (BFF в allowlist и не в нём).
- **Контексты** `orders` (Go), `payments` (Python), `restaurants` (TypeScript), `delivery` (Rust): второй verifier в существующем subject resolver. Assertion из metadata `x-nomnom-user-assertion` принимается, только если service identity вызывающего входит в `IDENTITY__USER_DELEGATES`; проверяются подпись по `IDENTITY__USER_JWKS_FILE`, `iss` = `IDENTITY__USER_ISSUER`, `aud` = `IDENTITY__AUDIENCE`, `exp`/`nbf` с допуском 30 s, `exp - iat ≤ 60 s`, непустой `sub`. Результат — `Subject{user, sub}`. Без параметров user assertions отклоняются, service-вызовы работают как раньше.
- **BFF**: проверка access token покупателя в плагине (JWKS identity provider, `iss`, `aud` BFF, срок; ID token отклоняется), минтер assertions с ключом из файла и gRPC client interceptor, который на каждый вызов контекста ставит service token BFF и assertion с audience этого контекста. Cosmo Router проверяет `Authorization` и передаёт его в плагин.
- **Развёртывание**: Helm values BFF (Secret с ключом подписи, конфигурация identity provider), контекстов (ConfigMap с JWKS BFF, три новых переменных); в kind — генерация ключа BFF и ConfigMap `bff-jwks` скриптом, как `cluster-jwks`.
- **Документация**: README контекстов и BFF, `docs/security.md` (уже ссылается на ADR 32).

Не входит: выбор identity provider и способ получения access token браузером (cookie, PKCE), resolvers customer API, gRPC-клиенты BFF к конкретным контекстам, Request Order Cancellation RPC, дедупликация `jti`.

## Capabilities

### New Capabilities

- `user-identity`: передача `user` subject от BFF в bounded contexts через BFF-signed user assertions.

### Modified Capabilities

Нет. Требования `service-identity` сохраняются без изменений: user assertion проверяется только после успешной service-проверки.

## Impact

- `tests/identity`: генератор и fixtures.
- `boundaries/{orders,payments,restaurants,delivery}`: resolver identity, конфигурация, тесты, Helm values, README.
- `boundaries/bff`: plugin (verifier access token, минтер, interceptor), Router config (`authentication.jwt`, header propagation), Helm values, README.
- `ops/kind`: скрипт ключа BFF и ConfigMap `bff-jwks`, values контекстов и BFF.
- Протоколы gRPC и схема SpiceDB не меняются.
