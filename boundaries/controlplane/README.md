# Control plane

Глобальный control plane cell-based архитектуры ([ADR 41](../../docs/adr/0041-cell-based-architecture-with-control-plane.md)):
контроллер Kubernetes на Go. Реестр Cell, Market и Market Placement — ресурсы `Cell` и `Market` группы
`controlplane.nomnom.dev/v1alpha1`, единственный источник правды. Своей базы данных и бизнес-API у него нет.
Язык — [GLOSSARY](GLOSSARY.md), локальные решения — [docs/adr](docs/adr).

Control plane не в горячем пути: ни gateway, ни ячейки к нему не обращаются. При старте, при изменении
`Cell` и `Market` и при изменении своих ресурсов на gateway контроллер server-side apply применяет в
неймспейс публичного gateway (`ROUTES__NAMESPACE`) полный набор ресурсов из реестра
([publish routes](slices/topology/applications/publishroutes/README.md)):

- `HTTPRoute market-<market_id>` — `/m/<market_id>` в Service Router Cell, префикс снимается,
  `X-Nomnom-Market` ставится из маршрута; метка `nomnom.dev/route: market`; маршруты Market с этой
  меткой и `app.kubernetes.io/managed-by: nomnom-controlplane`, которых нет в наборе (архивные),
  удаляются;
- `ConfigMap`, `HTTPRouteFilter` (`directResponse`) и `HTTPRoute markets` — Envoy сам отвечает на
  `GET /markets` списком `{"markets":[{"id","name","currency","timeZone"}]}` опубликованных Market по
  названию.

Очередь контроллера держит один ключ на всю топологию, поэтому публикации идут по одной, а следующее
событие применяет более новый снимок. Опроса по таймеру нет. Удалённый вручную маршрут возвращается без
перезапуска. Не применилось — `Ready=False` (`RoutesUnavailable`) и повтор с backoff до 30 секунд.

## Реестр

```yaml
apiVersion: controlplane.nomnom.dev/v1alpha1
kind: Cell
metadata:
  name: cell-1
spec:
  router: {namespace: nomnom-bff, name: bff, port: 3010}
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
```

Запись — `kubectl apply` от субъекта с `ClusterRole` `nomnom-topology-editor`, чтение — `kubectl get cells,markets`.

| Правило | Где проверяется | Нарушение |
| --- | --- | --- |
| Slug `^[a-z][a-z0-9-]{1,31}$` у имени `Cell` и `Market` | CEL | запись отклонена |
| Router: DNS-1123 label и порт 1..65535 | схема | запись отклонена |
| `displayName` 1..64 символа без пробелов по краям, `currency` ISO 4217, непустые `timeZone` и `cellRef` | схема, CEL | запись отклонена |
| `Cell.spec` и все поля `Market.spec`, кроме `archived`, неизменяемы | CEL | запись отклонена |
| `archived` не возвращается в `false` | CEL | запись отклонена |
| Удаление `Cell` и `Market` запрещено, `market_id` не используется повторно | `ValidatingAdmissionPolicy` | запись отклонена |
| Cell из `cellRef` объявлена | контроллер | `Ready=False`, `CellNotFound` |
| `timeZone` есть в базе IANA, встроенной в бинарь (`time/tzdata`) | контроллер | `Ready=False`, `InvalidTimeZone` |

Market с такой ошибкой не публикуется и не мешает остальным. Объявили недостающую Cell — он публикуется сам.
Архивный Market получает `Ready=False` с reason `Archived`.

Удаление защищено от случайности, а не от администратора кластера: тот может снять политику.

## Устройство

- `infrastructure/kubernetes/api/v1alpha1` — типы CRD; манифесты в `ops/helm/templates/crds` и DeepCopy генерирует `make generate`.
- `slices/topology` — домены `cell` и `market` (value objects в `vo/`), сценарий
  [`publishroutes`](slices/topology/applications/publishroutes/README.md), адаптеры `infrastructure/registry`
  (ресурсы реестра), `infrastructure/gatewayroutes` (маршруты через dynamic client) и `infrastructure/controller`
  (цикл reconcile на `controller-runtime`).
- DI — Google Wire (ADR 5): `di/wire.go`, `make generate`.
- Моки портов для тестов — mockery (шаблон testify), список в `.mockery.yml`, генерирует `make generate`.
- Runtime contract (ADR 17): технический порт `metrics` 9086 с `/health/*`; ready — после первой успешной
  публикации. OpenTelemetry (ADR 9) — OTLP metrics и traces, JSON-логи; Prometheus-эндпоинт `controller-runtime` выключен.
- Одна реплика без surge (`maxSurge: 0`, `maxUnavailable: 1`): новая версия стартует на месте старой. Короткое
  наложение на время остановки старого pod безопасно: обе применяют полный набор server-side apply, а следующее
  событие приводит ресурсы к реестру. Пока pod пересоздаётся, Envoy работает на последней конфигурации.

## Конфигурация

Обязательны `ROUTES__NAMESPACE`, `ROUTES__GATEWAY`, `ROUTES__LISTENER` (неймспейс маршрутов, Gateway и listener
публичного API; [config.example.yaml](config.example.yaml)): пустой параметр — процесс не стартует. Kubernetes API — только
in-cluster config ServiceAccount: вне кластера процесс не стартует (`make run` — только с поднятым в кластере API).

## Команды

`make run`, `make test` (контроллер на k3s в testcontainers, нужен Docker), `make lint`,
`make format`, `make format-check`, `make check`, `make generate`.

Развёртывание — Helm chart [`ops/helm`](ops/helm) (ADR 12); kind — [`ops/kind`](../../ops/kind/README.md).
