# Design

## Context

См. proposal.md — Why. У bounded contexts сейчас только минимальный HTTP bootstrap без бизнес-маршрутов; gRPC-контракты
(ADR 7), инфраструктурные адаптеры и образ BFF (ADR 10) ещё не реализованы. Helm charts приложений существуют (ADR 12),
probes в них выключены до появления health contract. ADR о gateway и CNI до этого изменения
не было, поэтому применены значения по умолчанию: Envoy Gateway и Cilium.

## Goals / Non-Goals

**Goals:**

- Доказать схему маршрутизации и изоляции на настоящем Kubernetes при минимальных ресурсах (один узел).
- Сделать окружение пригодным для CI: одна команда, таймауты, диагностика, очистка.

**Non-Goals:**

- Реализация бизнес-RPC, gRPC-клиентов в приложениях и образа BFF.
- Развёртывание настоящих Kafka, Temporal, PostgreSQL, MongoDB и Alloy.
- mTLS и авторизация identity вызывающей стороны.
- Production-топология gateway и выбор production CNI.

## Decisions

- **Envoy Gateway + Gateway API.** Стандартные `GRPCRoute` с match по service/method и
  переносимые ресурсы; Envoy сохраняет gRPC metadata и status. Альтернативы: service mesh
  (Istio/Linkerd) решает задачу без явного hop, но не даёт единой точки маршрутизации и
  дороже в эксплуатации; client-side discovery оставляет топологию в конфигурации callers.
- **Централизованный реестр маршрутов** в `nomnom-gateway` + `ReferenceGrant` в неймспейсе
  backend'а. Альтернатива — маршруты в неймспейсах владельцев: децентрализовано, но решения о
  retries расходятся и их сложнее ревьюить.
- **Маршрут на метод, retries — отдельным `GRPCRoute`.** `BackendTrafficPolicy` целится в
  route целиком, поэтому методы с retries группируются в отдельный route. Политика route
  заменяет политику Gateway, timeout повторяется явно.
- **`requestTimeout: 300s`.** Встроенный route timeout Envoy Gateway (15 s) обрезал бы
  длинные deadlines. Явный верхний предел оставляет управление deadline клиенту и
  ограничивает «вечные» вызовы.
- **Proxy в `envoy-gateway-system`, Service `internal-gateway`.** Режим по умолчанию Envoy
  Gateway; стабильное имя Service задаётся `EnvoyProxy`. Gateway Namespace Mode не используется,
  чтобы не зависеть от его ограничений.
- **Cilium + стандартный `NetworkPolicy`.** Hubble показывает вердикты `DROPPED`, а
  политики переносимы между CNI. `CiliumNetworkPolicy` не используется.
- **Skaffold для развёртывания.** Один `skaffold.yaml` описывает Helm-релизы платформы, сборку
  образов с тегом `inputDigest`, загрузку в kind, charts приложений, манифесты и ожидание
  readiness. Альтернативы: Tilt (лучший dev loop, но Tiltfile — снова код и registry вместо
  `kind load`), Helmfile (нет сборки и загрузки образов), Argo CD/Flux (для долгоживущих окружений).
- **Без тестовых сервисов-заглушек.** Проверки выполняются на реальных приложениях из
  pod `net-probe`: `orders` предоставляет первый gRPC API, остальные каркасы пока HTTP.
- **Пробы проверяются поведением.** После применения политик проверяются Available, рестарты
  и события `Unhealthy` (Envoy Proxy, CoreDNS, Cilium).

## Risks / Trade-offs

- [Kubernetes 1.37 вне протестированных матриц Cilium 1.20 и Envoy Gateway 1.9] →
  совместимость подтверждается `make kind-e2e`; при регрессии откатить `KIND_NODE_IMAGE` на 1.36.
- [Не у всех приложений включены probes] → `orders` использует отдельный management port;
  остальные приложения добавляются после реализации их runtime contract.
- [Проверки gateway первого business RPC неполны] → route уже опубликован, а trace context,
  deadlines и retries проверяются после подключения вызывающего клиента и identity adapter.
- [Skaffold не подставляет environment в версии charts] → версии charts закреплены в
  `skaffold.yaml`, остальные — в `versions.env`; предзагрузка образов берёт список из
  `skaffold render`, поэтому версии не дублируются.
- [Gateway — общая точка отказа] → в production несколько реплик data plane на разных узлах; в kind одна реплика.
- [Single-node кластер не проверяет cross-node трафик CNI] → при необходимости добавить worker-узлы в `cluster.yaml`.
- [Envoy Gateway tracing выключен] → при включении gateway станет span'ом: `trace-id` сохранится,
  `parent-id` изменится; проверку trace context нужно будет строить на сравнении `trace-id`.

## Migration Plan

Изменение аддитивное. Новый RPC bounded context публикуется записью `GRPCRoute` и
`ReferenceGrant`, порт `grpc` и label `nomnom.dev/business-api=true` его pod; заблокированные
проверки 1, 4 и 5 включаются на этом RPC.
