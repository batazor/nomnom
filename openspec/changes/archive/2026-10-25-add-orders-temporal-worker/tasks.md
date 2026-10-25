# Tasks

## 1. Конфигурация

- [x] 1.1 Добавить `temporal.*` и `fulfillment.*` в `pkg/config` с defaults, env и валидацией; unit-тесты (AC-448); обновить `config.example.yaml`.

## 2. Компоненты worker'а

- [x] 2.1 Добавить `fulfillorder.EnsureStageSearchAttribute` и тесты на `operatorservicemock` (AC-449, AC-450).
- [x] 2.2 Добавить `slices/order/infrastructure/neighbours` (`Unavailable`, `Simulated`) и тесты (AC-452).
- [x] 2.3 Вынести store слайса в провайдер `NewStore`.

## 3. Composition root и процесс

- [x] 3.1 Добавить `TemporalWorker` (lifecycle.Worker) и тесты lifecycle; Wire injector `InitializeWorker`, `make generate`.
- [x] 3.2 Добавить `cmd/orders-worker`; Dockerfile, Makefile `run-worker`.

## 4. Проверка и развёртывание

- [x] 4.1 Integration-тест против Temporal dev server и `make test-integration` (AC-446); ручная проверка SIGTERM (AC-447) и недоступного Temporal (AC-451).
- [x] 4.2 Helm: `command`, `service.enabled`, `values-worker.yaml`; `helm lint` и `helm template`.
- [x] 4.3 Обновить README `orders`, `fulfillorder` и `ops/README.md`; `make check`, `openspec validate add-orders-temporal-worker --strict`.
