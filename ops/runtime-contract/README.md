# Runtime contract tests

Black-box проверка [runtime contract](../../docs/cloud-native-runtime.md) ([ADR 17](../../docs/adr/0017-cloud-native-application-runtime.md)). Один набор сценариев запускается против реального процесса каждого backend-приложения (`orders`, `restaurants`, `delivery`, `payments`) и видит его только снаружи: environment, бизнес- и технический порты, сигналы и exit code. Реализация lifecycle в каждом языке своя, контракт — общий.

```bash
make runtime-contract                               # все приложения
make runtime-contract RUNTIME_SERVICES="orders payments"
```

Нужны toolchains приложений (`go`, `node`, `cargo`, `uv`) и `python3` 3.9+ без дополнительных пакетов. Порты выбираются свободные и передаются через `SERVER__PORT` и `MANAGEMENT__PORT`; бюджет shutdown — `SHUTDOWN__READINESS_DELAY=1000ms`, `SHUTDOWN__DRAIN_TIMEOUT=5000ms`.

| Сценарий | Что проверяется |
| --- | --- |
| `test_startup_live_ready_on_management_port` | `/health/startup`, `/health/live`, `/health/ready` отвечают `200` на техническом порту; бизнес-порт отвечает, но health на нём нет. |
| `test_sigterm_drains_and_exits_zero` | После `SIGTERM` readiness сразу `503` с `shutting_down`, liveness `200`; во время readiness delay бизнес-порт обслуживает запросы, после — перестаёт принимать соединения; idle keep-alive не задерживает выход; exit `0`. |
| `test_sigint_exits_zero` | `SIGINT` ведёт к той же штатной остановке, exit `0`. |
| `test_invalid_config_fails_fast` | Невалидная конфигурация: non-zero exit, технический порт не открывается. |
| `test_startup_failure_exits_non_zero` | Занятый бизнес-порт: non-zero exit без перехода в ready. |

Завершение начатых запросов в пределах `drain_timeout` и non-zero exit при его превышении проверяются unit-тестами lifecycle-модуля каждого приложения: у каркасов пока нет медленных бизнес-операций, а test-only endpoints в production-код не добавляются.
