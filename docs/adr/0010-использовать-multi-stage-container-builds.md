# 10. Использовать multi-stage container builds

Date: 2026-10-24

## Status

Accepted

## Context

BiteRoute содержит запускаемые приложения на Go, TypeScript/Node.js, Rust и Python.
Сборочные toolchain, генераторы и зависимости разработки не должны попадать в production
образы. При этом минимизация образа не должна ломать TLS, конфигурацию, OpenTelemetry,
Pyroscope и символизацию нативных профилей.

Каждый bounded context сейчас является самостоятельным приложением со своими manifest и
lockfile. Корневого workspace и общих build-time контрактов пока нет. BFF использует
WunderGraph Cosmo Router с Go-плагином, но версия загружаемого router binary в репозитории
не закреплена.

## Decision

Для запускаемых bounded contexts использовать multi-stage Dockerfile в корне приложения
и тот же каталог как build context. Обязательные именованные стадии — `build` и `runtime`;
дополнительные стадии зависимостей добавляются, когда они отделяют редко меняющиеся
manifest/lockfile от исходного кода.

Зависимости устанавливаются строго по lockfile. Кеши Go, npm, Cargo и uv подключаются как
BuildKit cache mounts и не становятся слоями runtime. Если результат Cargo build находится
в cache mount, бинарник копируется из него в обычный путь в той же `RUN`-команде.

Runtime содержит только приложение, production-зависимости, необходимые системные
библиотеки и CA certificates. Процессы запускаются непривилегированными пользователями и
через exec-form `ENTRYPOINT`/`CMD`. Если runtime не устанавливает обработчик SIGTERM для
PID 1, минимальный init пересылает сигнал дочернему процессу. Конфигурация передаётся через
environment или read-only mount; `.env`, ключи и токены исключаются из build context.
Приватные зависимости и BSR доступны только через BuildKit secrets, когда они появятся.

Выбор runtime по приложениям:

- `orders` — статический `CGO_ENABLED=0` Go-бинарник в distroless static с CA;
- `restaurants` — скомпилированный JavaScript, production npm dependencies и Node.js;
- `delivery` — Debian runtime, совместимый с glibc build-образа, с CA; release-бинарник не
  strip-ится и содержит debug info для символизации Pyroscope;
- `payments` — одинаковая версия Python и семейство Debian в build/runtime, переносимый
  uv virtualenv и исходники приложения.

Dockerfile BFF откладывается до закрепления версии и checksum Cosmo Router. Существующий
`boundaries/bff/plugins/bff/Dockerfile` создаёт только plugin executable и не считается образом всего
BFF. После pinning отдельный build должен создавать plugin и execution config, а runtime —
содержать router binary и необходимые конфигурационные файлы.

## Consequences

Production-образы не содержат компиляторы и dev dependencies, сборки лучше используют кеш
и воспроизводятся по lockfiles. Языковые runtime могут различаться: единый минимальный base
не навязывается ценой совместимости с libc, TLS или observability.

Patch-версии base images и сборочных инструментов требуется регулярно обновлять вместе с
manifest/lockfile и повторной проверкой сборки, запуска, пользователя и обработки SIGTERM.
Docker Compose, Kubernetes manifests и CI/CD остаются отдельными решениями. Для BFF до
pinning router artifact контейнеризация остаётся неполной.

## Примечание (2026-10-25)

Условие для BFF выполнено: [`boundaries/bff/Dockerfile`](../../boundaries/bff/Dockerfile) закрепляет
Cosmo Router `router@0.363.0` и SHA-256 его release-архивов для `linux/amd64` и `linux/arm64`;
при несовпадении checksum сборка падает. Stage `plugin` собирает plugin, `config` — `config.json`
закреплённым `wgc`, runtime — distroless static nonroot с router, plugin, `config.json` и
production `config.yaml`. Образ разворачивается в kind (OpenSpec change `deploy-bff-in-kind`).
