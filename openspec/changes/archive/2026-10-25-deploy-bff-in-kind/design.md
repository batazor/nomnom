# Design

## Decisions

### 1. Закрепление Cosmo Router

Версия Router (сейчас локально 0.363.0) и SHA-256 архива для `linux/amd64` и `linux/arm64` записываются в `boundaries/bff/router.versions` (или `ARG` с комментарием-источником); build stage скачивает release и проверяет checksum, при несовпадении сборка падает. Выбирается последний стабильный release на момент реализации, совместимость подтверждается запуском, а не предположением.

### 2. Образ

```text
plugin  (golang)   go build плагина → bin/<os>_<arch>
config  (node)     npm ci, wgc router compose → config.json   (или закоммиченный config.json, если wgc требует сети)
router  (alpine)   скачивание и проверка router
runtime (distroless/static, nonroot)  router + plugins/bff/bin + config.json + config.production.yaml
```

Production config отличается от dev: `listen_addr: 0.0.0.0:3010`, нет `dev_mode` и playground, то же правило `headers.all.request: propagate Authorization`. Health probes — штатные `/health/live` и `/health/ready` Router.

### 3. Identity в kind

- Ключ подписи (P-256) создаётся скриптом один раз и хранится только в Secret кластера; повторный запуск переиздаёт `bff-jwks` из существующего ключа.
- Тестовый identity provider — ключ `tests/identity/fixtures/idp-signing-key.pem` и его JWKS; проверка выпускает access token этим ключом. Это допустимо только для kind и прямо помечено в values и README.
- Контексты принимают user assertions только от ServiceAccount BFF (`nomnom-bff/bff=bff` в allowlist, `userDelegates: bff`).

### 4. Сеть

BFF вызывает контексты только через internal gateway (ADR 15/16): egress BFF разрешён к gateway и DNS; GRPCRoute-маршруты `orders` и `restaurants` уже есть. Входящий трафик к BFF в kind — через `kubectl port-forward` в проверке.

### 5. Проверка

Сценарий в скриптах `ops/kind` (рядом с существующими проверками gateway) выполняет GraphQL-запросы к BFF и проверяет ответы по spec delta; при успехе отмечаются задачи 4.1 и 4.3 `add-user-identity-assertions`.

## Risks / Trade-offs

- [`wgc router compose` требует сети при сборке] → допускается использовать закоммиченный `config.json`, проверенный `npm run check`.
- [Общий кластер `nomnom-test` используется другими сессиями] → только `make kind-deploy`/`kind-test` поверх существующего кластера, без пересоздания.
