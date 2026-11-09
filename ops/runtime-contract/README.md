# Runtime contract tests

Black-box проверка [runtime contract](../../docs/cloud-native-runtime.md) ([ADR 17](../../docs/adr/0017-cloud-native-application-runtime.md)). Один набор сценариев запускается против реального процесса каждого backend-приложения (`orders`, `restaurants`, `delivery`, `payments`) и видит его только снаружи: файл конфигурации, секреты в environment, бизнес- и технический порты, сигналы и exit code. Реализация lifecycle в каждом языке своя, контракт — общий.

```bash
make runtime-contract                               # все приложения
make runtime-contract RUNTIME_SERVICES="orders payments"
```

Нужны toolchains приложений (`go`, `node`, `cargo`, `uv`) и `python3` 3.9+ без дополнительных пакетов. Каждый запуск получает временный файл конфигурации (`CONFIG_FILE`, `apiVersion: config.nomnom.dev/v1alpha1`, kind сервиса, роль в `metadata.name`), секреты и `POD_NAME` в environment; несекретные `SECTION__KEY` и `OTEL_*` из окружения тестов процессу не передаются. Порты выбираются свободные и задаются `spec.server.port` и `spec.management.port`; бюджет shutdown — `spec.shutdown.readinessDelay: 1000ms`, `spec.shutdown.drainTimeout: 5000ms`. Файл пишется в JSON — подмножестве YAML.

| Сценарий | Что проверяется |
| --- | --- |
| `test_startup_live_ready_on_management_port` | `/health/startup`, `/health/live`, `/health/ready` отвечают `200` на техническом порту; бизнес-порт отвечает, но health на нём нет. |
| `test_sigterm_drains_and_exits_zero` | После `SIGTERM` readiness сразу `503` с `shutting_down`, liveness `200`; во время readiness delay бизнес-порт обслуживает запросы, после — перестаёт принимать соединения; idle keep-alive не задерживает выход; exit `0`. |
| `test_sigint_exits_zero` | `SIGINT` ведёт к той же штатной остановке, exit `0`. |
| `test_invalid_config_fails_fast` | Невалидная конфигурация (`drainTimeout: 0ms`): non-zero exit, технический порт не открывается. |
| `test_unknown_key_fails_fast` | Неизвестный ключ `spec.shutdown.drainTimout`: non-zero exit, ошибка называет ключ. |
| `test_missing_config_file_fails_fast` | Нет `CONFIG_FILE`: non-zero exit. |
| `test_non_secret_env_is_ignored` | `SHUTDOWN__DRAIN_TIMEOUT=0ms` в environment не перекрывает файл: процесс переходит в ready. |
| `test_missing_postgres_dsn_fails_fast` | `orders` без секрета `POSTGRES__DSN`: non-zero exit. |
| `test_missing_spicedb_endpoint_fails_fast` | Пустой `spec.spicedb.endpoint`: non-zero exit. |
| `test_startup_failure_exits_non_zero` | Занятый бизнес-порт: non-zero exit без перехода в ready. |

Завершение начатых запросов в пределах `drainTimeout` и non-zero exit при его превышении проверяются unit-тестами lifecycle-модуля каждого приложения: у каркасов пока нет медленных бизнес-операций, а test-only endpoints в production-код не добавляются.
