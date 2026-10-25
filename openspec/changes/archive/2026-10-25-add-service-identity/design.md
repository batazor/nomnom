# Design

## Context

См. ADR 27. Каждый контекст уже имеет порт `SubjectResolver` и `RejectingSubjectResolver`; клиенты `orders` подключаются к internal gateway без credentials.

## Decisions

### Контракт проверки (одинаковый в четырёх языках)

| Проверка | Отказ |
| --- | --- |
| metadata `authorization` = `Bearer <jwt>` | нет или другой scheme |
| header `alg` ∈ {RS256, ES256}, `kid` есть в JWKS | `none`, HS*, неизвестный `kid` |
| подпись | неверная |
| `iss` == `IDENTITY__ISSUER` | иначе |
| `aud` (строка или массив) содержит `IDENTITY__AUDIENCE` | иначе |
| `exp` > now − 30 s, `nbf` ≤ now + 30 s | просрочен / ещё не действителен |
| `sub` есть в `IDENTITY__SERVICE_ACCOUNTS` (`ns/sa=name`, через запятую) | неизвестный ServiceAccount |

Любой отказ — gRPC `UNAUTHENTICATED` без деталей причины клиенту; причина пишется в log. Результат — `Subject{kind: service, id: name}`.

Если `IDENTITY__ISSUER`, `IDENTITY__AUDIENCE` или `IDENTITY__JWKS_FILE` пусты — `RejectingSubjectResolver` и предупреждение при старте. Нечитаемый JWKS, пустой allowlist при заданном issuer или неверный формат allowlist — ошибка startup.

### Библиотеки

Go — `github.com/golang-jwt/jwt/v5`; Python — `PyJWT[crypto]`; TypeScript — `jose`; Rust — `jsonwebtoken`. Каждая конфигурируется явным списком алгоритмов.

### Клиент orders

`grpc.PerRPCCredentials`: читает `IDENTITY__TOKEN_DIR/<audience>` при каждом вызове (`nomnom.payments`, `nomnom.restaurants`, `nomnom.delivery` по сервису метода); отсутствующий файл — ошибка вызова (retryable). Пустой `IDENTITY__TOKEN_DIR` — вызовы без credentials. TLS не требуется: credentials помечаются как не требующие transport security внутри кластера (ADR 16).

### Развёртывание

Helm chart каждого контекста: `identity.audience`, `identity.issuer`, `identity.serviceAccounts`, `identity.jwksConfigMap` (mount в `/etc/nomnom/identity/jwks.json`), `identity.tokenAudiences` (projected `serviceAccountToken` в `/var/run/secrets/nomnom/identity/<audience>`, `expirationSeconds: 3600`). kind: скрипт создаёт ConfigMap `cluster-jwks` в namespace приложений из `kubectl get --raw /openid/v1/jwks` и берёт issuer из `/.well-known/openid-configuration`.

### Тестовые векторы

`tests/identity/generate.sh` создаёт тестовый RSA-ключ, `jwks.json` и `vectors.json` (accepted: `valid`, `valid_string_aud`; rejected: expired, wrong audience/issuer, unknown subject/kid, not yet valid, bad signature, `alg: none`). Unit-тесты каждого контекста проходят по всем векторам.

## Risks / Trade-offs

- [Тестовый приватный ключ в репозитории] → только для векторов, помечен в скрипте; production ключи принадлежат кластеру.
- [Ротация signing key кластера] → новые tokens отклоняются до обновления ConfigMap; скрипт kind пересоздаёт его.
- [In-memory authorization денаит] → identity проходит, но доступ решают permissions; SpiceDB — отдельный change.
