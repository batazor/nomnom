# Proposal

## Why

Реестр control plane — несколько редко меняющихся записей о топологии, а не бизнес-данные. Ради них boundary держит PostgreSQL, gRPC API и проверку SpiceDB, а вызывают этот API только seed и e2e kind.

У императивной модели есть и прямые недостатки:

- удалённый вручную `HTTPRoute` возвращается только после следующей записи или перезапуска;
- если применить ресурсы не удалось, команду повторяет оператор;
- топологию нельзя описать в git: `cell-1` и `berlin` создаёт Job, потому что к установке bootstrap control plane ещё не запущен.

Control plane и так работает с Kubernetes API и пишет ресурсы Gateway API. Реестр естественно становится ресурсами Kubernetes, а публикация маршрутов — циклом reconcile.

## What Changes

- **BREAKING** Реестр — cluster-scoped CRD `Cell` и `Market` группы `controlplane.nomnom.dev/v1alpha1`; они единственный источник правды.
- **BREAKING** Удаляются gRPC API `nomnom.controlplane.v1.ControlPlaneService`, база PostgreSQL `controlplane` и проверка `platform#manage_markets`. Доступ к реестру задаёт Kubernetes RBAC.
- Control plane становится контроллером: следит за `Cell`, `Market` и своими ресурсами на публичном gateway и приводит их к реестру. Дрейф и сбои применения исправляются без действий оператора.
- Формат и неизменяемость полей проверяет API server (CRD schema и CEL). Существование Cell и допустимость часового пояса проверяет контроллер и сообщает результат в `status`.
- Архивирование — `spec.archived: true`. Удаление `Cell` и `Market` запрещает `ValidatingAdmissionPolicy`: `market_id` не переиспользуется.
- В kind `cell-1` и `berlin` объявлены ресурсами chart `ops/kind/demo`, Market прогона e2e создаётся и архивируется через `kubectl`.

Не входит:

- перенос Market между Cell и Previous Placement: `cellRef` остаётся неизменяемым;
- webhook для синхронной проверки часового пояса и Cell;
- CRD для других частей топологии и поддержка нескольких кластеров;
- перенос данных из PostgreSQL: в репозитории описан только kind с `emptyDir`, топология объявляется заново.

## Capabilities

### New Capabilities

- `controlplane-topology`: ресурсы `Cell` и `Market`, их проверка, архивирование, `status`, доступ и состав процесса control plane.

### Modified Capabilities

- `cell-routing`: Router Cell переезжает в `controlplane-topology`, публикация маршрутов переходит с записи и старта на reconcile, чтение реестра перестаёт требовать SpiceDB.

## Impact

- `boundaries/controlplane`: типы CRD, контроллер, `slices/topology`, `di`, `pkg/config`, helm chart. Удаляются `infrastructure/grpc`, `infrastructure/migration`, миграции и репозитории, `slices/topology/infrastructure/spicedb`, `pkg/authz`, сценарии `registercell`, `createmarket`, `archivemarket`, `listmarkets`, `permission`.
- `authorization/schema.zed`, `authorization/validation.yaml`: удаляется `platform#manage_markets`.
- `ops/kind`: bootstrap (база и роль `controlplane`), `values/controlplane.yaml`, маршруты и политики internal gateway, NetworkPolicy, chart `demo` и seed, e2e `controlplane` и `order-lifecycle`, README.
- Документация: ADR 41, README и GLOSSARY `controlplane`.
- Новая зависимость: `sigs.k8s.io/controller-runtime`.
