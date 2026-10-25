# application-runtime Specification

## Purpose
Определяет наблюдаемое снаружи поведение процесса backend-приложения nomnom в container lifecycle: health, остановку и exit code. Требования одинаковы для всех языков и проверяются black-box сценариями `ops/runtime-contract`.

## Requirements

### Requirement: Health на техническом порту

Процесс SHALL обслуживать `GET /health/startup`, `GET /health/live` и `GET /health/ready` на техническом порту `management.port`, который MUST отличаться от бизнес-порта. Бизнес-порт MUST NOT обслуживать health endpoints. Ответ `200` означает успех, `503` — отказ; тело — JSON со `status` и при отказе `reason` (`initializing`, `shutting_down`).

#### Scenario: Экземпляр запущен

- **WHEN** процесс завершил инициализацию и открыл бизнес-порт
- **THEN** все три endpoint отвечают `200`, а бизнес-порт принимает запросы

#### Scenario: Health не публикуется на бизнес-порту

- **WHEN** клиент запрашивает `/health/ready` на бизнес-порту
- **THEN** ответ не `200`

### Requirement: Graceful shutdown по сигналу

По `SIGTERM` или `SIGINT` процесс SHALL сразу перевести readiness в `503` с `reason` `shutting_down`, сохраняя liveness `200`, обслуживать запросы в течение `shutdown.readiness_delay`, затем прекратить приём новых соединений, дождаться начатых запросов в пределах `shutdown.drain_timeout` и завершиться с exit `0`. Idle keep-alive соединения MUST NOT задерживать выход до drain deadline.

#### Scenario: Штатная остановка

- **WHEN** процессу отправлен `SIGTERM`
- **THEN** readiness `503`, liveness `200`, бизнес-порт отвечает до истечения readiness delay и перестаёт принимать соединения после него, процесс завершается с exit `0`

#### Scenario: Превышен drain deadline

- **WHEN** начатая работа не завершилась за `shutdown.drain_timeout`
- **THEN** оставшиеся соединения закрываются и процесс завершается с non-zero exit

### Requirement: Fail fast при старте

Процесс SHALL завершаться с non-zero exit без перехода в ready при невалидной конфигурации или невозможности открыть порт.

#### Scenario: Невалидная конфигурация

- **WHEN** `SHUTDOWN__DRAIN_TIMEOUT=0ms`
- **THEN** процесс завершается с non-zero exit, технический порт не открывается

#### Scenario: Бизнес-порт занят

- **WHEN** бизнес-порт уже занят другим процессом
- **THEN** процесс завершается с non-zero exit
