# Design

## Context

См. `proposal.md`, spec delta `user-identity` и [ADR 32](../../../docs/adr/0032-user-identity-via-bff-signed-assertions.md). Каждый контекст уже аутентифицирует `service` identity по ADR 27: `authorization: Bearer <projected ServiceAccount token>`, JWKS кластера из ConfigMap `cluster-jwks` (в kind её создаёт `ops/kind/scripts/identity-jwks.sh`), allowlist `IDENTITY__SERVICE_ACCOUNTS`, fail closed без конфигурации. Реализации на четырёх языках сверены векторами `tests/identity/fixtures/vectors.json`, которые генерирует `tests/identity/generate.sh`.

BFF — Cosmo Router и Go router plugin (`boundaries/bff/plugins/bff`). Router передаёт заголовки клиента в плагин как gRPC metadata в нижнем регистре по правилам `headers` (propagate/set); JWT authentication Router по умолчанию читает `Authorization: Bearer`. Внутренних gRPC-клиентов у BFF пока нет.

## Goals / Non-Goals

**Goals:**

- `user` subject в контекстах только из проверяемого assertion и только от BFF;
- одинаковые правила на Go, Python, TypeScript и Rust, сверенные общими векторами;
- BFF готов подписывать assertions для будущих resolvers.

**Non-Goals:**

- identity provider, login flow браузера, session и refresh;
- resolvers и клиенты BFF к конкретным контекстам;
- отзыв assertions и дедупликация `jti`.

## Decisions

### 1. Порядок проверки в контексте

```text
authorization ─► service identity (ADR 27) ── нет ──► UNAUTHENTICATED
                         │ да
x-nomnom-user-assertion отсутствует ──► Subject{service, name}
                         │ присутствует
service ∉ IDENTITY__USER_DELEGATES ──► UNAUTHENTICATED
                         │
assertion невалиден ──► UNAUTHENTICATED
                         │
                         └──► Subject{user, sub}
```

Assertion без service token не рассматривается вовсе. Повторный или пустой `x-nomnom-user-assertion` — `UNAUTHENTICATED`. Service identity BFF не теряется: resolver кладёт её рядом с subject в контекст вызова для log и audit (поле `Delegate` или аналог языка), но use cases и SpiceDB видят `user:<sub>`.

Альтернатива — отдельный resolver только для user. Отклонена: тогда assertion можно было бы принять без service identity BFF, что нарушает BFF ADR 2.

### 2. Конфигурация

| Переменная | Назначение | По умолчанию |
| --- | --- | --- |
| `IDENTITY__USER_ISSUER` | ожидаемый `iss` assertion | не задано |
| `IDENTITY__USER_JWKS_FILE` | JWKS BFF | не задано |
| `IDENTITY__USER_DELEGATES` | services, которым разрешено передавать user, через запятую | пусто |

User assertions принимаются, только если заданы issuer и JWKS и список делегатов не пуст; иначе любой assertion отклоняется, а вызов без assertion обрабатывается как раньше. Неразбираемый JWKS — ошибка startup. Audience — существующий `IDENTITY__AUDIENCE`.

### 3. Проверка assertion

- `alg` только ES256; `none`, HMAC и RS* отклоняются; `kid` обязателен и есть в JWKS.
- `iss` равен настроенному; `aud` (строка или массив) содержит audience контекста.
- `exp` и `nbf` с допуском 30 s, как ADR 27; `iat` обязателен; `exp - iat ≤ 60 s` — защита от долгоживущих assertions при ошибке минтера.
- `sub` непустая строка без пробелов по краям; формат идентификатора provider не интерпретируется.
- `jti` обязателен и попадает в log отклонения/принятия, но не дедуплицируется.

### 4. Векторы

`tests/identity/generate.sh` дополняется второй секцией, не меняя `vectors.json` service identity:

- `bff-signing-key.pem` (P-256) и `bff-jwks.json`;
- `user-vectors.json`: `issuer`, `audience` (`nomnom.payments`, как у service-векторов), `now`, `delegates`, `service_issuer`, `service_accounts` (с `nomnom/bff=bff`), `delegate_token` и `non_delegate_token` (service tokens `bff` и `orders-worker`, подписанные ключом кластера из `jwks.json`), `expected_subject`, `accepted`, `rejected`, `tokens`;
- `idp-signing-key.pem`, `idp-jwks.json`, `idp-vectors.json` — access tokens тестового identity provider для BFF, включая ID token, который нельзя принять.

Assertion живёт не дольше 60 s, поэтому статический вектор не может быть валиден в реальном времени: реализации проверяют `user-vectors.json` с часами, зафиксированными на `now`. Service tokens делегата действуют до 2100 года и проверяются по реальным часам, как раньше.

### 5. BFF

- **Access token покупателя.** Router: `authentication.jwt` по JWKS identity provider и правило `headers.all.request: propagate Authorization`. Плагин читает metadata `authorization`, проверяет подпись (RS256/ES256 по JWKS provider), `iss`, `aud` BFF, срок и отсутствие признаков ID token (`typ` не `JWT`/`at+jwt` или наличие `nonce` без `scope` — правило фиксируется в коде и тестах). Конфигурация: `BFF__IDP_ISSUER`, `BFF__IDP_AUDIENCE`, `BFF__IDP_JWKS_FILE`. Первая задача — проверить в kind, что `authorization` доходит до плагина; если Router его не пропускает (известный issue Cosmo), Router передаёт проверенный `sub` правилом `set` по claims в `x-nomnom-verified-sub`, а плагин принимает его только с loopback-соединения Router.
- **Минтер.** `UserAssertions.Sign(sub, audience) → jwt`: ES256 по ключу `BFF__ASSERTION_KEY_FILE` с `kid` из `BFF__ASSERTION_KEY_ID`, `iss: nomnom-bff`, `exp = iat + 30 s` (меньше лимита 60 s), `jti` — случайный UUID. Ключ читается при старте; невалидный ключ — ошибка startup.
- **Interceptor.** gRPC unary client interceptor для будущих клиентов: берёт `sub` из контекста запроса, ставит `authorization: Bearer <service token BFF для audience>` (файл `IDENTITY__TOKEN_DIR/<audience>`, как у `orders`) и `x-nomnom-user-assertion`. Без пользователя в контексте вызов идёт только с service token.

### 6. Развёртывание

- Kind: `ops/kind/scripts/bff-identity.sh` создаёт ключ один раз на кластер, Secret `bff-assertion-key` и ConfigMap `bff-jwks`; skaffold вызывает его рядом с `identity-jwks.sh`. Ключ не попадает в Git.
- Helm контекстов: `identity.userJwksConfigMap`, `identity.userIssuer`, `identity.userDelegates`; без ConfigMap переменные не задаются.
- Helm BFF: Secret ключа как файл, projected ServiceAccount tokens для audience контекстов, конфигурация identity provider.

## Risks / Trade-offs

- [Router не передаёт `Authorization` в плагин] → спайк первой задачей и запасной вариант с `set` по claims.
- [Расхождение проверок между языками] → общие векторы обязательны для всех четырёх контекстов.
- [Ключ BFF утёк] → ротация по ADR 32: новый `kid` в JWKS контекстов, затем переключение BFF.
- [Нет реального identity provider в kind] → тесты используют тестовый ключ provider из `tests/identity`; e2e с provider — отдельный change.

## Migration Plan

1. Векторы.
2. Verifier во всех контекстах (могут идти параллельно, по одному на язык).
3. BFF: спайк Router → plugin, verifier access token, минтер, interceptor.
4. Kind и Helm.
5. Документация. Rollback: убрать user-переменные — контексты снова отклоняют assertions, service-вызовы не затронуты.
