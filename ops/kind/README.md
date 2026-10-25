# Тестовое окружение kind

kind используется для интеграционных тестов, которым нужен Kubernetes, и для E2E: проверки
internal gateway, NetworkPolicy, kubelet probes и установки Helm charts. Unit-тесты доменной
и прикладной логики от кластера не зависят и запускаются `check` каждого контекста.
Решение — [ADR 14](../../docs/adr/0014-использовать-kind-для-тестового-окружения-kubernetes.md).

## Требования

Docker, `kind` и `skaffold` версий из [`versions.env`](versions.env), `kubectl`, Helm 3.8+ (OCI charts), `jq`.

## Команды

Из корня репозитория:

```bash
make kind-e2e       # весь цикл для CI: кластер → skaffold run → проверки → удаление
make kind-up        # single-node кластер, предзагрузка образов платформы, Cilium + Hubble, Envoy Gateway
make kind-deploy    # skaffold run: сборка образов, загрузка в kind, gateway, charts приложений, readiness
make kind-dev       # skaffold dev: пересборка и передеплой при изменении исходников
make kind-test      # проверки; NetworkPolicy применяются внутри (фаза A без них, фаза B с ними)
make kind-diagnostics
make kind-down
```

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `KIND_CLUSTER_NAME` | `nomnom-test` | имя кластера; контекст всегда `kind-${KIND_CLUSTER_NAME}` |
| `BFF_HOST_PORT` | `18080` | host-порт для NodePort `30080` публичного API BFF |
| `KIND_KEEP` | `0` | `1` — не удалять кластер после `make kind-e2e` |
| `PRELOAD_PLATFORM_IMAGES` | `1` | загружать образы платформы из Docker-кеша хоста вместо pull на узле |

Все команды явно используют контекст `kind-${KIND_CLUSTER_NAME}`. Отчёт проверок и
диагностика сохраняются в `ops/kind/.artifacts/`.

## Как устроено

- `scripts/cluster.sh` создаёт кластер kind из [`cluster.yaml`](cluster.yaml) (один узел,
  default CNI отключён, `extraPortMappings` для BFF) и загружает в узел образы платформы.
  Список образов берётся из `skaffold render --module platform`, образы скачиваются один раз в
  Docker хоста, поэтому повторные прогоны не ходят за ними в сеть.
- [`skaffold/skaffold.yaml`](skaffold/skaffold.yaml), модуль `platform`: Helm-релизы Cilium
  (Hubble включён) и Envoy Gateway (с Gateway API CRD); hook сверяет версию CRD с `versions.env`.
- Модуль `apps` (требует `platform`): сборка образов bounded contexts их multi-stage Dockerfile
  с тегом `inputDigest` (хеш входов сборки), загрузка в kind без registry, charts приложений
  ([ADR 12](../../docs/adr/0012-helm-charts-per-deployable-application.md)) с values из
  [`values/`](values/) и `pullPolicy: Never`, манифесты gateway и pod проверок. Skaffold status
  check ждёт readiness workloads, hook — `Accepted`/`Programmed` у GatewayClass, Gateway и
  `BackendTrafficPolicy`.
- `scripts/verify.sh` выполняет проверки, `scripts/diagnostics.sh` собирает pod logs, events,
  состояние Gateway API, `cilium-dbg status` и DROPPED flows Hubble; `scripts/e2e.sh`
  связывает шаги и собирает диагностику при ошибке.

## Версии

| Что | Где |
| --- | --- |
| kind, node image (digest), Skaffold, ожидаемая версия Gateway API CRD, образ Envoy Proxy, образ проверок | [`versions.env`](versions.env) |
| charts Cilium и Envoy Gateway | [`skaffold/skaffold.yaml`](skaffold/skaffold.yaml): Skaffold не подставляет environment в `remoteChart`/`version` |

Политика — последние стабильные релизы. Kubernetes 1.37 новее диапазонов, протестированных
Cilium 1.20 и Envoy Gateway 1.9 (1.33–1.36); совместимость подтверждается `make kind-e2e`.

## Неймспейсы

| Неймспейс | Содержимое |
| --- | --- |
| `nomnom-orders`, `nomnom-restaurants`, `nomnom-delivery`, `nomnom-payments` | приложения из Helm charts; в `orders` и `restaurants` — pod проверок `net-probe` (curl) |
| `nomnom-bff` | зарезервирован: образ BFF ещё не определён (ADR 10) |
| `nomnom-gateway` | GatewayClass, Gateway, `BackendTrafficPolicy`, `EnvoyProxy`, реестр `GRPCRoute` |
| `envoy-gateway-system` | controller Envoy Gateway и Envoy Proxy, Service `internal-gateway` |
| `nomnom-outsider` | pod `net-probe` вне прикладного контура для ingress-проверок |

## Internal gateway

Схема, маршрутизация и политики описаны в
[ADR 15](../../docs/adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md),
[ADR 16](../../docs/adr/0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md) и
разделе [синхронного взаимодействия](../../README.md#синхронное-взаимодействие).

Адрес gateway для приложений: `INTERNAL_GATEWAY_ADDR=internal-gateway.envoy-gateway-system.svc.cluster.local:8080`.

Реестр маршрутов — `GRPCRoute` в `nomnom-gateway`. Бизнес-RPC bounded contexts ещё не
реализованы, поэтому реестр пуст и любой вызов получает 404 (gRPC `UNIMPLEMENTED`). Новый
бизнес-RPC публикуется так:

1. `GRPCRoute` в `nomnom-gateway` с `method.type: Exact`, полным именем сервиса и методом;
   файл добавляется в `manifests.rawYaml` модуля `apps`. Методы с автоматическими retries —
   в отдельном route с собственной `BackendTrafficPolicy` (политика route заменяет политику
   Gateway, поэтому `timeout.http.requestTimeout: 300s` повторяется).
2. `ReferenceGrant` в неймспейсе backend'а для `GRPCRoute` из `nomnom-gateway`.
3. Порт pod с именем `grpc` и label `nomnom.dev/business-api: "true"` в values окружения.

Retries сейчас не включены ни для одного метода.

## Проверки

`make kind-test` выполняет:

| # | Проверка | Статус |
| --- | --- | --- |
| A | без NetworkPolicy `orders → restaurants` и `outsider → restaurants` напрямую отвечают 200, gateway доступен | выполняется |
| 1 | вызов бизнес-API через gateway достигает backend | **заблокирована**: нет gRPC API и маршрутов |
| 2 | с NetworkPolicy прямые `orders ↔ restaurants` получают silent drop (curl timeout, не DNS и не refused) при выполненных условиях: backend Available, имя резолвится, gateway доступен; Hubble фиксирует `DROPPED`; `nomnom-outsider` не достаёт ни backend, ни gateway | выполняется; условие «тот же вызов через gateway проходит» **заблокировано** |
| 3 | неизвестный gRPC-сервис через gateway → HTTP 404 (`UNIMPLEMENTED`) | выполняется |
| 4 | `traceparent` caller'а совпадает с полученным backend'ом | **заблокирована** |
| 5 | короткий deadline → `DEADLINE_EXCEEDED`, длинный не обрезается; retries только у идемпотентных методов | **заблокирована**; route timeout 300 s задан `BackendTrafficPolicy` |
| 6 | инфраструктурные подключения под политиками | DNS выполняется; Kafka, Temporal, PostgreSQL, MongoDB, Alloy **заблокированы**: не развёрнуты, приложения их не используют |
| 6a | после применения политик pod Available, без рестартов и проваленных kubelet-проб (Envoy Proxy, CoreDNS, Cilium) | выполняется; пробы приложений выключены до health contract (ADR 12) |
| E2E | через публичный API BFF | **заблокирована**: образ BFF не определён (ADR 10); port mapping подготовлен |

### Технические подключения

| Назначение | Источник → получатель | Порт |
| --- | --- | --- |
| Kubelet probes | узел → pod | порт пробы; разрешено Cilium для host-трафика, проверяется тестом 6a |
| DNS | pod → CoreDNS | UDP/TCP 53 |
| Telemetry | приложения → Alloy | 4317 OTLP gRPC, 4318 OTLP HTTP, 4040 Pyroscope |
| Метрики приложений | Alloy → pod | порт с именем `metrics` |
| Метрики Envoy | Alloy → Envoy Proxy | 19001 |
| xDS | Envoy Proxy → controller Envoy Gateway | 18000 |
