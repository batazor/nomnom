# Tasks

- [x] `orders`: `pkg/lifecycle`, `management.port`, `shutdown.readiness_delay`, `App` как `Worker`, unit-тесты.
- [x] `restaurants`: `pkg/lifecycle/lifecycle.ts`, `HttpWorker`, конфигурация shutdown и management, unit-тесты.
- [x] `delivery`: `pkg/lifecycle`, `HttpWorker`, конфигурация, unit-тесты.
- [x] `payments`: `pkg/lifecycle`, `HttpWorker`, конфигурация, unit-тесты.
- [x] Black-box сценарии `ops/runtime-contract` и `make runtime-contract`.
- [x] `make runtime-contract` проходит для всех четырёх приложений.
- [x] Обновить `docs/cloud-native-runtime.md` и README приложений.
- [x] Helm charts `restaurants`, `delivery`, `payments`: порт `metrics`, `startupProbe`/`livenessProbe`/`readinessProbe`, `terminationGracePeriodSeconds`, library chart `bitnami/common`.
- [x] Helm chart `orders`: те же изменения, порт `management` переименован в `metrics`.
- [ ] Прогон `make kind-e2e` с обновлёнными charts.
