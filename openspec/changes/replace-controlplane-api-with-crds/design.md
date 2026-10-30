# Design

## Ресурсы

Оба ресурса cluster-scoped: control plane один на систему, а Cell и Market не принадлежат неймспейсу. Идентификатор — `metadata.name`.

```yaml
apiVersion: controlplane.nomnom.dev/v1alpha1
kind: Cell
metadata:
  name: cell-1
spec:
  router:
    namespace: nomnom-bff
    name: bff
    port: 3010
---
apiVersion: controlplane.nomnom.dev/v1alpha1
kind: Market
metadata:
  name: berlin
spec:
  displayName: Berlin
  currency: EUR
  timeZone: Europe/Berlin
  cellRef: cell-1
  archived: false
status:
  observedGeneration: 1
  conditions:
    - type: Ready
      status: "True"
      reason: RoutesApplied
```

`kubectl get markets` показывает колонки Cell, Currency, Time Zone, Archived и Ready. Архивные Market в этом выводе остаются: фильтра на стороне API server нет.

## Где что проверяется

| Правило | Где | Результат нарушения |
| --- | --- | --- |
| Slug `^[a-z][a-z0-9-]{1,31}$` у `metadata.name` | CEL | запись отклонена |
| `router.namespace`, `router.name` — DNS-1123 label, `port` 1..65535 | schema | запись отклонена |
| `displayName` 1..64 без пробелов по краям, `currency` `^[A-Z]{3}$`, непустой `timeZone` и `cellRef` | schema, CEL | запись отклонена |
| `Cell.spec` неизменяем | CEL `self == oldSelf` | запись отклонена |
| Поля `Market.spec`, кроме `archived`, неизменяемы; `archived` не возвращается в `false` | CEL | запись отклонена |
| `DELETE` запрещён | `ValidatingAdmissionPolicy` | запись отклонена |
| Cell из `cellRef` существует | контроллер | `Ready=False`, `CellNotFound` |
| `timeZone` есть в базе IANA | контроллер (`time/tzdata`) | `Ready=False`, `InvalidTimeZone` |

CEL не обращается к другим объектам и не знает базы IANA, поэтому две последние проверки асинхронны. Так оператор видит часть ошибок не при `kubectl apply`, а в `status`. Webhook это исправил бы, но добавил бы новый компонент в путь записи и управление сертификатами. Пока асинхронной проверки достаточно: Market с такой ошибкой просто не публикуется.

`Market` с невалидными данными не получает маршрута и не попадает в список, но не мешает остальным Market. Если Cell появится позже, Market опубликуется сам.

## Reconcile

Контроллер один на всю топологию. Любое событие `Cell`, `Market` или управляемого ресурса на gateway ставит в очередь один и тот же ключ, а обработка идёт по одной:

1. прочитать все `Cell` и `Market` из кеша;
2. проверить `cellRef` и `timeZone`;
3. построить полный набор ресурсов тем же кодом, что сейчас в `infrastructure/gatewayroutes`, и применить его server-side apply;
4. удалить управляемые `HTTPRoute` Market, которых нет в наборе;
5. записать `status` тех `Cell` и `Market`, у которых он изменился.

Один ключ заменяет advisory lock PostgreSQL: параллельных применений нет. Устаревший кеш может дать устаревший снимок, но следующее событие приведёт ресурсы к новому. При ошибке применения `Market` получает `Ready=False`, `RoutesUnavailable`, а ключ возвращается в очередь с backoff.

Управляемые ресурсы отбираются меткой `app.kubernetes.io/managed-by: nomnom-controlplane` в неймспейсе `ROUTES__NAMESPACE`. Опроса по таймеру нет: восстановление запускает watch.

Запись `status` не должна запускать новый цикл: контроллер фильтрует события по `metadata.generation`.

### Запуск и несколько реплик

- Readiness становится `true` после первого успешного reconcile. Если первый reconcile не удался, контроллер повторяет его, а Deployment не считается готовым.
- Обязательный конфиг, в том числе `ROUTES__*`, по-прежнему проверяется на старте: пустое значение останавливает процесс.
- Leader election через `Lease` в неймспейсе release: при rolling update две версии не должны применять ресурсы одновременно.
- Встроенный metrics server `controller-runtime` выключен. Телеметрия остаётся OTLP (ADR 9), `/health/*` — на техническом порту 9086 (ADR 17).

## Доступ

Запись в реестр — обычный Kubernetes RBAC: chart поставляет `ClusterRole` `nomnom-topology-editor`, а кому её выдать, решает кластер.

ServiceAccount control plane получает:

- `get`, `list`, `watch` на `cells`, `markets` и запись только в `cells/status`, `markets/status`;
- текущие права на ресурсы gateway в `ROUTES__NAMESPACE` и дополнительно `list`, `watch`, нужные для watch;
- `Lease` в своём неймспейсе.

Менять `spec` реестра он не может. Вместо аудита через SpiceDB и user assertion остаётся аудит Kubernetes API.

`ValidatingAdmissionPolicy` защищает от случайного удаления, а не от администратора кластера: тот может снять политику. В текущей модели администратор тоже мог изменить строку в БД, поэтому это не новая дыра, но гарантия `market_id` теперь держится на настройке кластера, а не на схеме БД.

## Установка

- CRD и политика лежат в `templates/` chart `controlplane`, а не в `crds/`: Helm не обновляет `crds/` при upgrade. Аннотация `helm.sh/resource-policy: keep` не даёт удалить CRD при `helm uninstall`: вместе с ним исчезли бы все Market и память о занятых id.
- `Cell` и `Market` kind — ресурсы chart `ops/kind/demo`, который и так ставится после `controlplane`. У них тоже `helm.sh/resource-policy: keep`, иначе `helm uninstall demo` упрётся в запрет удаления.
- Seed больше не регистрирует топологию; `seed.json` хранит только `market` для ресторанов и курьеров. Контексты не проверяют Market в control plane, поэтому порядок относительно маршрута для seed не важен.
- e2e Market прогона создаётся `kubectl apply`, а архивируется `kubectl patch`. Операции chainsaw `apply` и `create` не подходят: они удаляют созданный ресурс в cleanup, а удаление запрещено.

## Тесты

Правила CEL и политика имеют смысл только на настоящем API server, поэтому тесты контроллера идут на k3s через testcontainers (модуль `testcontainers-go`) с установленными CRD Gateway API и Envoy Gateway. Чистую логику построения ресурсов и проверки значений покрывают обычные unit-тесты. In-memory клиента `controller-runtime` (`fake`) нет.

## Устройство пакетов

- `infrastructure/kubernetes/api/v1alpha1` — Go-типы CRD; манифесты и DeepCopy генерирует `controller-gen` через `make generate`.
- `slices/topology/domains/cell`, `market` остаются: value objects проверяют значения контроллера, а правила `Repeat` и ошибки уникальности удаляются.
- `slices/topology/applications/publishroutes` становится сценарием reconcile.
- `slices/topology/infrastructure/gatewayroutes` остаётся адаптером применения маршрутов.

## Перенос Market

Перенос остаётся стратегией из ADR 41. CRD его не закрывают: когда он понадобится, правило неизменяемости `cellRef` ослабится, а Previous Placement появится в `spec`, чтобы история размещений жила в git. Шаги переноса останутся у worker Temporal, а его состояние попадёт в `status`.

## Риски

- **Потеря etcd теряет реестр.** Объявления остаются в git, но память об архивных Market, созданных не из git, теряется. В kind это e2e Market с уникальными id прогона, поэтому риска нет; вне kind нужен бэкап etcd.
- **Изменения реестра доступны всем, у кого есть `nomnom-topology-editor`.** Сценарий продуктовой админки потребует отдельного API поверх CR.
- **Один кластер.** Реестр живёт в кластере control plane. Cell в другом кластере остаётся backend маршрута, как и записано в ADR 41, но несколько control plane не предусмотрены.

## Порядок

Сначала в chart и код добавляются CRD и контроллер, затем переводятся kind и e2e, и последними удаляются gRPC, PostgreSQL и SpiceDB. Переходного режима, где реестр читается из двух источников, нет: контроллер сразу работает только с CRD. Проверка — один раз в конце.

Открытый change `add-scheduled-orders` добавляет в `cell-routing` требование `FR-689` о `time_zone` в `CreateMarket`. Этот change отменяет его: проверку пояса задаёт `FR-737`. `add-scheduled-orders` архивируется первым, после чего сюда добавляется `REMOVED` для `FR-689`. Формат `GET /markets` с `timeZone` из того change не меняется.
