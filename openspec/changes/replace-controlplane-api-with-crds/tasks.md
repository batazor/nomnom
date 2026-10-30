# Tasks

## 1. Ресурсы

- [x] 1.1 Go-типы `Cell` и `Market` в `infrastructure/kubernetes/api/v1alpha1`, правила CEL, printer columns; `controller-gen` в `make generate` (`FR-734`, `FR-735`, `FR-736`).
- [x] 1.2 Chart `controlplane`: CRD и `ValidatingAdmissionPolicy` запрета удаления в `templates/` с `helm.sh/resource-policy: keep` (`FR-736`).
- [x] 1.3 Chart `controlplane`: RBAC control plane, `ClusterRole` `nomnom-topology-editor` (`FR-738`).

## 2. Контроллер

- [x] 2.1 Сценарий reconcile на основе `publishroutes`: снимок реестра, проверка `cellRef` и `timeZone`, применение, удаление лишних маршрутов, запись `status` (`FR-630`, `FR-737`).
- [x] 2.2 Watch `Cell`, `Market` и управляемых ресурсов gateway, один ключ очереди, фильтр по `generation`, backoff; одна реплика и `maxSurge: 0` в chart (`FR-630`, `FR-631`).
- [x] 2.3 Readiness после первого успешного reconcile, OTLP-телеметрия вместо metrics server `controller-runtime`, `di` и `cmd` (`FR-631`, `FR-739`).

## 3. Kind

- [x] 3.1 Chart `ops/kind/demo`: ресурсы `cell-1` и `berlin`, убрать топологию из `seed.py`, `seed.json` и `seed.yaml` (`FR-734`, `FR-735`).
- [x] 3.2 e2e `controlplane`: декларативная топология, отказы API server, запрет удаления, права, восстановление маршрута без перезапуска (`AC-1001`–`AC-1016`).
- [x] 3.3 e2e `order-lifecycle`: Market прогона через `kubectl apply` и `kubectl patch`, без автоудаления chainsaw.
- [x] 3.4 e2e `cell-routing`: проверки FR-630, FR-631 и FR-633 на новой модели (`AC-806`–`AC-809`, `AC-813`, `AC-814`).

## 4. Удаление

- [x] 4.1 `boundaries/controlplane`: `infrastructure/grpc`, `infrastructure/migration`, миграции и репозитории, `slices/topology/infrastructure/spicedb`, `pkg/authz`, `pkg/config/database`, сценарии `registercell`, `createmarket`, `archivemarket`, `listmarkets`, `permission`, конфиг `IDENTITY__*`, `SPICEDB__*`, `POSTGRES__DSN`, бизнес-порт и Service; `buf.yaml`, `buf-generate`, образ `e2e/probe` (`FR-739`).
- [x] 4.2 `authorization/schema.zed` и `validation.yaml`: удалить `platform#manage_markets` (`FR-738`).
- [x] 4.3 `ops/kind`: база и роль `controlplane` в bootstrap, `values/controlplane.yaml`, маршруты и политики `controlplane-v1-*` internal gateway, NetworkPolicy к PostgreSQL и SpiceDB, делегаты и токены `nomnom.controlplane` в `demo` и `e2e-driver`.

## 5. Документация

- [x] 5.1 ADR 41: решение, развёртывание, стратегия расширения, последствия.
- [x] 5.2 README и GLOSSARY `controlplane`, README сценариев, `ops/kind/README.md`.

## 6. Проверка

- [x] 6.1 `make check` в `boundaries/controlplane`, проверка схемы SpiceDB (`authorization/validation.yaml`), kind e2e; `openspec validate replace-controlplane-api-with-crds --strict`.
- [ ] 6.2 Перед архивом: когда `add-scheduled-orders` заархивирован, добавить в delta `cell-routing` `REMOVED` для `FR-689`.
