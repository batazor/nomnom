# Tasks

## 1. Общее

- [x] 1.1 `platform#operate_simulator` в `authorization/schema.zed` и `validation.yaml` (`FR-604`, `AC-762`); GRPCRoute `*-v1-simulator` в `ops/kind/manifests/gateway/gateway.yaml`.

## 2. Сервисы

- [x] 2.1 `restaurants`: слайс `simulator`, RPC, коллекция состояния, `PeriodicLoop`, конфиг (`FR-604`–`FR-606`, `AC-761`–`AC-765`).
- [x] 2.2 `delivery`: слайс `simulator`, RPC, миграция, worker, конфиг.
- [x] 2.3 `orders`: слайс `simulator`, RPC, миграция, worker, конфиг, `SIMULATOR__CUSTOMER_ID`.

## 3. BFF и UI

- [x] 3.1 BFF: перегенерировать клиентов, `simulator`/`setSimulatorEnabled`, слайс `simulator`, тесты (`FR-607`, `AC-766`).
- [x] 3.2 UI: переключатель «Эмулятор» в шапке, моки, тесты (`FR-607`, `AC-766`, `AC-767`).

## 4. Проверка

- [ ] 4.1 Проверки каждого сервиса, `openspec validate add-demo-simulator --strict`, прогон в kind.
