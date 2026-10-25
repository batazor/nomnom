# Proposal

## Why

BFF — единственная точка входа клиентов, но в тестовом кластере kind его нет: в `boundaries/bff` нет запускаемого образа (ADR 10 откладывал его до закрепления версии и checksum Cosmo Router), а в skaffold нет release. Поэтому нельзя проверить насквозь через internal gateway ни экран заказа, ни user identity ADR 32; в change `add-user-identity-assertions` открыты задачи 4.1 (ключ подписи и `bff-jwks` в kind) и 4.3 (сквозная проверка).

## What Changes

- **Образ BFF** `boundaries/bff/Dockerfile`: закреплённые версия и SHA-256 Cosmo Router; build stages собирают плагин и `config.json`; непривилегированный runtime содержит только router, плагин, `config.json` и production `config.yaml` (`listen_addr: 0.0.0.0:3010`, без `dev_mode`, правило передачи `Authorization`).
- **kind**:
  - release `bff` в skaffold (namespace `nomnom-bff`), artifact `nomnom/bff`;
  - `ops/kind/scripts/bff-identity.sh`: ключ подписи user assertions — один раз на кластер (Secret `bff-assertion-key` в `nomnom-bff`), ConfigMap `bff-jwks` в namespaces контекстов; тестовый identity provider — ConfigMap `idp-jwks` из `tests/identity/fixtures` (только kind);
  - values: BFF (`internalGateway.address`, identity, projected tokens `nomnom.orders`, `nomnom.restaurants`), контексты (`identity.userJwksConfigMap: bff-jwks`, `userIssuer: nomnom-bff`, `userDelegates: bff`, ServiceAccount BFF в allowlist);
  - NetworkPolicy BFF: egress только к internal gateway и DNS; доступ к BFF для проверок — port-forward;
  - сквозная проверка в `make kind-test`/`kind-e2e`: access token тестового provider, `bffInfo`, `order(id)` без token — ошибка аутентификации, с token для отсутствующего заказа — `null` (значит, assertion прошёл в `orders`), чужой workload с тем же assertion — `UNAUTHENTICATED`.
- Документация: README BFF (контейнеризация, развёртывание), `ops/kind/README.md`, ADR 10 — ссылка на закреплённую версию.
- Закрываются задачи 4.1 и 4.3 change `add-user-identity-assertions`.

Не входит: production identity provider и login flow браузера, публикация BFF наружу через public gateway, Cosmo control plane.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `kubernetes-test-environment`: BFF развёрнут в kind и проверяется насквозь.

## Impact

`boundaries/bff` (Dockerfile, production config, Helm при необходимости), `ops/kind` (skaffold, scripts, values, netpol, проверки), values контекстов, `openspec/changes/add-user-identity-assertions/tasks.md`, документация.
