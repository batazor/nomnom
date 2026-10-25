# Proposal

## Why

[ADR 17](../../../docs/adr/0017-cloud-native-application-runtime.md) и [документ cloud-native runtime](../../../docs/cloud-native-runtime.md) задают единый lifecycle процесса, но composition roots `orders`, `restaurants`, `delivery` и `payments` напрямую поднимают HTTP-серверы: health state нет, технический порт не открыт, readiness-propagation delay не выдерживается, а `payments` и `restaurants` не обрабатывают `SIGTERM`. Без этого rolling update теряет запросы, а Helm probes нельзя включить.

## What Changes

- В каждом приложении добавить собственный lifecycle-модуль (`pkg/lifecycle`) с маленьким интерфейсом: конфигурация lifecycle + `Worker` (start, run, shutdown с deadline).
- Lifecycle владеет техническим портом с `/health/startup`, `/health/live`, `/health/ready`, health state, обработкой `SIGTERM`/`SIGINT`, readiness delay, drain и exit code.
- Добавить конфигурацию `management.port`, `shutdown.readiness_delay`; `shutdown.drain_timeout` появляется во всех приложениях.
- Добавить общий black-box набор сценариев `ops/runtime-contract`, запускаемый против реальных процессов всех четырёх приложений, и цель `make runtime-contract`.
- Не входит: Helm charts (probes, порт `metrics`, `terminationGracePeriodSeconds`), OpenTelemetry flush, gRPC health, Kafka/Temporal workers, BFF.

## Capabilities

### New Capabilities

- `application-runtime`: единое наблюдаемое поведение процесса backend-приложения в container lifecycle.

### Modified Capabilities

Нет.

## Impact

- `boundaries/{orders,restaurants,delivery,payments}`: `pkg/lifecycle`, конфигурация, composition root (`cmd`/`entrypoints`), unit-тесты, README.
- `ops/runtime-contract/`, корневой `Makefile`, `docs/cloud-native-runtime.md`.
- Новых runtime-зависимостей нет.
