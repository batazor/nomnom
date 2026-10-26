# 14. Использовать kind для тестового окружения Kubernetes

Date: 2026-10-24

## Status

Accepted

Связан с [12. Helm Charts per Deployable Application](0012-helm-charts-per-deployable-application.md)

Связан с [15. Использовать internal gateway для синхронных вызовов бизнес-API](0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md)

Связан с [16. Запрещать обход internal gateway через Cilium и NetworkPolicy](0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md)

## Context

Сетевые правила, маршрутизация internal gateway, NetworkPolicy, kubelet probes и
развёртывание Helm charts нельзя проверить unit-тестами или Docker Compose: им нужен
настоящий Kubernetes API, CNI с поддержкой NetworkPolicy и трафик между узлами. Окружение
должно запускаться одинаково локально и в CI, без облачной учётной записи и без
интерактивных шагов.

Unit-тесты доменной и прикладной логики от кластера не зависят и продолжают выполняться
командами `check` каждого контекста.

## Decision

Использовать [kind](https://kind.sigs.k8s.io/) для интеграционных тестов, которым нужен
Kubernetes, и для E2E. Окружение находится в `ops/kind/` и управляется целями `kind-*`
корневого `Makefile`. Платформа и приложения разворачиваются [Skaffold](https://skaffold.dev/)
(`ops/kind/skaffold/skaffold.yaml`); кластер создаёт kind CLI.

- Кластер из одного узла: control-plane без taint запускает и workloads, что экономит
  ресурсы локальной машины и CI. Default CNI отключён (`disableDefaultCNI: true`); CNI устанавливается отдельно.
  `extraPortMappings` публикует NodePort `30085` публичного gateway — webhooks внешних систем
  и публичный API клиентов ([ADR 41](0041-cell-based-architecture-with-control-plane.md)) — на
  `127.0.0.1:${WEBHOOKS_HOST_PORT}`.
- Имя кластера параметризуется `KIND_CLUSTER_NAME`, по умолчанию `nomnom-test`. Все
  команды явно используют контекст `kind-${KIND_CLUSTER_NAME}` и отказываются работать с
  другим кластером.
- Версии kind, node image (с digest), Skaffold, Gateway API CRD и образов закреплены в
  `ops/kind/versions.env`, версии charts Cilium и Envoy Gateway — в Helm-релизах
  `skaffold.yaml`, потому что Skaffold не подставляет environment в версии charts.
  Политика выбора — последние стабильные релизы; установленная версия Gateway API CRD
  сверяется с `versions.env`.
- Skaffold собирает образы существующими multi-stage Dockerfile ([ADR 10](0010-использовать-multi-stage-container-builds.md))
  с тегом `inputDigest` (хеш входов сборки) и загружает их в kind без registry; workloads
  используют `imagePullPolicy: Never`. Образы платформы скачиваются один раз в Docker хоста
  и загружаются в узел, поэтому повторные прогоны не тянут их из сети.
- Bounded contexts устанавливаются собственными charts ([ADR 12](0012-helm-charts-per-deployable-application.md))
  с values тестового окружения из `ops/kind/values/`.
- Readiness workloads ожидается Skaffold status check, статусы `Accepted`/`Programmed`
  Gateway API — hook'ом Skaffold, оба с таймаутом. При ошибке сохраняются pod logs, events, состояние Gateway API,
  `cilium-dbg status` и DROPPED flows Hubble в `ops/kind/.artifacts/`.
- `make kind-e2e` выполняет весь цикл неинтерактивно и удаляет кластер; `KIND_KEEP=1`
  сохраняет его для отладки.

Проверки, которым нужны бизнес-RPC, инфраструктура или BFF, выполняются по мере появления
соответствующих компонентов; до этого `make kind-test` выводит их как заблокированные.

## Consequences

Сетевые и gateway-правила проверяются автоматически на настоящем Kubernetes с той же
моделью NetworkPolicy, что и в целевых кластерах. Локальный прогон и CI используют одну
команду и одни версии.

Полный прогон требует Docker, нескольких ГБ памяти и загрузки образов платформы, поэтому
он медленнее unit-тестов и не входит в `check` контекстов. Single-node кластер не проверяет
политики на трафике между узлами (encapsulation/routing CNI); при необходимости в
`cluster.yaml` добавляются worker-узлы. kind не воспроизводит облачные load balancers, storage classes и реальную задержку сети. Политика «последние
версии» может опережать протестированные upstream матрицы совместимости: на дату решения
Kubernetes 1.37 новее диапазонов, заявленных Cilium 1.20 и Envoy Gateway 1.9, и
совместимость подтверждается прогоном окружения. Пока у bounded contexts нет gRPC API и
инфраструктурных адаптеров, проверки вызова через gateway, trace context, deadlines, retries и
подключений к Kafka, Temporal, PostgreSQL, MongoDB и Alloy заблокированы.
