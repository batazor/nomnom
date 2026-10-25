# Tasks

## 1. Общие векторы

- [ ] 1.1 Дополнить `tests/identity/generate.sh`: ключ BFF P-256, `bff-jwks.json`, `user-vectors.json` (accepted, rejected: expired, wrong_audience, wrong_issuer, ttl_too_long, empty_sub, missing_jti, bad_signature, unknown_kid, alg_none, hs256), service account `bff` в векторах ADR 27; тестовый ключ и JWKS identity provider для BFF; перегенерировать fixtures и проверить, что существующие service-векторы не изменились (`FR-472`).

## 2. Verifier в контекстах

- [ ] 2.1 `orders` (Go): user verifier в `infrastructure/grpc`, конфигурация `IDENTITY__USER_*`, проверка делегата, `Subject{user}` с сохранением service identity для log; тесты на `user-vectors.json` и сценарии делегата (`FR-471`–`FR-473`, `AC-525`–`AC-529`).
- [ ] 2.2 `payments` (Python): то же в resolver identity; тесты на общих векторах (`FR-471`–`FR-473`).
- [ ] 2.3 `restaurants` (TypeScript): то же в resolver identity; тесты на общих векторах (`FR-471`–`FR-473`).
- [ ] 2.4 `delivery` (Rust): то же в resolver identity; тесты на общих векторах (`FR-471`–`FR-473`).

## 3. BFF

- [ ] 3.1 Спайк: в kind проверить, что Cosmo Router с `authentication.jwt` и правилом propagate передаёт `authorization` в плагин; зафиксировать результат в README BFF; при отказе реализовать запасной вариант из design (решение 5).
- [ ] 3.2 Verifier access token в плагине (`BFF__IDP_ISSUER`, `BFF__IDP_AUDIENCE`, `BFF__IDP_JWKS_FILE`), отказ ID token; тесты на тестовом ключе provider (`AC-531`).
- [ ] 3.3 Минтер assertions (`BFF__ASSERTION_KEY_FILE`, `BFF__ASSERTION_KEY_ID`, срок 30 s, `jti`) и проверка, что `sub` берётся только из проверенного token; тест, что assertion проходит verifier `orders` на тех же векторах (`FR-474`, `AC-530`).
- [ ] 3.4 gRPC unary client interceptor: service token BFF для audience контекста и assertion из контекста запроса; без пользователя — только service token; тесты.

## 4. Развёртывание

- [ ] 4.1 `ops/kind/scripts/bff-identity.sh`: ключ BFF один раз на кластер, Secret `bff-assertion-key`, ConfigMap `bff-jwks`; вызов из skaffold рядом с `identity-jwks.sh`; values kind контекстов (`userJwksConfigMap`, `userIssuer`, `userDelegates: bff`) и BFF.
- [ ] 4.2 Helm charts контекстов: `identity.userJwksConfigMap`, `identity.userIssuer`, `identity.userDelegates` → `IDENTITY__USER_*`; Helm BFF: Secret ключа, projected tokens для audience контекстов, конфигурация provider; `helm lint` и `helm template` всех charts.
- [ ] 4.3 Проверить в kind: вызов `orders` от BFF с assertion даёт `Subject{user}` (по log), тот же assertion от другого workload — `UNAUTHENTICATED`.

## 5. Документация и проверка

- [ ] 5.1 README `orders`, `payments`, `restaurants`, `delivery` и BFF (переменные, порядок проверки, ротация ключа), `ops/kind/README.md`; проверить Markdown links.
- [ ] 5.2 Прогнать тесты и линтеры всех затронутых приложений, `openspec validate add-user-identity-assertions --strict` и `git diff --check`.

## Workflow follow-up

- Resolver `order(id)` в BFF поверх interceptor.
- Выбор identity provider и login flow браузера.
- После review реализации архивировать change через OpenSpec archive workflow.
