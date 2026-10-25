# 16. Запрещать обход internal gateway через Cilium и NetworkPolicy

Date: 2026-10-24

## Status

Accepted

Связан с [15. Использовать internal gateway для синхронных вызовов бизнес-API](0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md)

## Context

Правило [ADR 15](0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md)
нельзя обеспечить только соглашением: Kubernetes по умолчанию разрешает любому pod
подключаться к любому другому pod и Service. Прямой вызов backend'а в обход gateway
обходит маршруты, timeouts, retries и будущие политики доступа и не виден в общем реестре.

Kubernetes NetworkPolicy — переносимый механизм, но применяется только CNI, который его
поддерживает. Ранее CNI в проекте не выбирался.

## Decision

Использовать [Cilium](https://cilium.io/) как CNI тестового окружения и стандартные
Kubernetes `NetworkPolicy` как механизм запрета обхода. Cilium-специфичные CRD не
используются, чтобы политики оставались переносимыми; Hubble используется для диагностики
вердиктов.

Прикладные неймспейсы имеют label `nomnom.dev/tier=app`. В каждом действуют:

| Политика | Назначение |
| --- | --- |
| `default-deny-all` | запрет всего ingress и egress pod неймспейса |
| `allow-business-api-from-internal-gateway` | ingress к pod с `nomnom.dev/business-api=true` на порт `grpc` только от Envoy Proxy internal gateway |
| `allow-egress-to-internal-gateway` | egress к Envoy Proxy internal gateway на порт 8080 |
| `allow-dns-egress` | egress к CoreDNS, UDP/TCP 53 |
| `allow-telemetry-egress` | egress к Alloy: OTLP 4317/4318, Pyroscope 4040 |
| `allow-metrics-scrape-from-alloy` | ingress от Alloy на порт `metrics` |
| `allow-infrastructure-egress` | egress владельца к своей инфраструктуре (см. ниже) |

Envoy Proxy выбирается по namespace `envoy-gateway-system` и labels
`gateway.envoyproxy.io/owning-gateway-name=internal`,
`gateway.envoyproxy.io/owning-gateway-namespace=nomnom-gateway`.

Инфраструктурные подключения по владельцам:

| Приложение | Разрешённые подключения |
| --- | --- |
| `orders` | PostgreSQL 5432, Kafka 9092, Temporal Server 7233 |
| `restaurants` | MongoDB 27017, Kafka 9092 |
| `delivery`, `payments` | Kafka 9092; хранилища не выбраны ([ADR 8](0008-storage-strategy-postgresql-and-mongodb.md)) |
| `bff` | только internal gateway и Alloy |

Для pod Envoy Proxy internal gateway: ingress на 8080 только из прикладных неймспейсов,
ingress метрик Envoy (19001) только от Alloy; egress только к бизнес-API backend'ов (порт
`grpc`), DNS и xDS controller Envoy Gateway (18000).

Политики выбирают только прикладные workloads и data plane gateway. Системные компоненты
(`kube-system`, controller Envoy Gateway) политиками не ограничиваются. Kubelet probes
приходят с адреса узла; в Cilium такой трафик разрешён по умолчанию, и это поведение
проверяется тестом после применения политик, а не предполагается.

## Consequences

Обход gateway технически невозможен для pod прикладных неймспейсов, а прямые подключения
видны в Hubble как `DROPPED`. Политики переносимы на другой CNI с поддержкой NetworkPolicy,
но поведение host-трафика (kubelet probes) и обработка Service ClusterIP зависят от CNI и
должны проверяться заново при его смене.

Каждый новый порт, инфраструктурная зависимость или технический endpoint требует явного
правила; ошибка в политике проявляется как timeout, поэтому окружение kind проверяет как
разрешённые, так и запрещённые пути. NetworkPolicy ограничивает сеть, но не подтверждает
identity вызывающего: mTLS и авторизация остаются отдельным решением. Production CNI
выбирается вместе с платформой кластера; это решение фиксирует Cilium для тестового
окружения и модель политик для всех окружений.
