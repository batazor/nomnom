# Tasks

## 1. Тестовый кластер и платформа

- [x] 1.1 Закрепить версии в `ops/kind/versions.env` и проверить, что `make kind-up` отклоняет несовпадающий kind
- [x] 1.2 Описать single-node кластер без default CNI с `extraPortMappings` и проверить один узел после `make kind-up`
- [x] 1.3 Установить Cilium с Hubble и Envoy Gateway; проверить readiness узлов, CoreDNS и совпадение версии Gateway API CRD

## 2. Сборка и развёртывание

- [x] 2.1 Собирать образы существующими Dockerfile с тегом `inputDigest` и загружать в kind через Skaffold (`make kind-deploy`)
- [x] 2.2 Устанавливать bounded contexts их Helm charts с values `ops/kind/values/` и `pullPolicy: Never`
- [x] 2.3 Ожидать readiness workloads (Skaffold status check) и статусов GatewayClass/Gateway/BackendTrafficPolicy (hook) с таймаутом
- [x] 2.4 Собирать диагностику при ошибке и удалять кластер (`make kind-e2e`, `make kind-diagnostics`)

## 3. Internal gateway

- [x] 3.1 Gateway с ClusterIP Service `internal-gateway`; проверить 404 (`UNIMPLEMENTED`) для неизвестного gRPC-сервиса (проверка 3)
- [x] 3.2 Явный route timeout 300s в `BackendTrafficPolicy` Gateway; проверить `Accepted` политики
- [ ] 3.3 Первый `nomnom.orders.v1.OrdersService` опубликован в `GRPCRoute`; проверить в kind маршрутизацию, metadata, status, `traceparent`, deadlines и retries (проверки 1, 4, 5) — маршрутизация, metadata, status, `traceparent` и deadline проверены (1.1, 2.4, 4.1, 4.2, 5.1; вызывающий `nomnom-bff/grpc-caller` с projected token, traces orders → `trace-capture`); открыто: retries — в реестре нет методов с включёнными retries

## 4. Сетевая изоляция

- [x] 4.1 NetworkPolicy прикладных неймспейсов и gateway; A/B-проверка прямого вызова `orders ↔ restaurants` с Hubble `DROPPED` (проверка 2)
- [x] 4.2 Проверить DNS, kubelet-пробы и системные компоненты под политиками (проверки 6.1 и 6a)
- [x] 4.3 Проверить подключения к Kafka, Temporal, PostgreSQL, MongoDB и Alloy под политиками (проверки 6.2–6.10): PostgreSQL, Kafka, Temporal и изоляция владельцев проверены; MongoDB не развёрнута, Alloy проверяется только с `GRAFANA_CLOUD=1`

## 5. Документация

- [x] 5.1 ADR 14–16 через adr-tools; README, `ops/kind/README.md` и `boundaries/README.md` описывают окружение, схему вызовов и политики

## Workflow follow-up

- Добавить E2E через публичный API BFF после появления образа BFF.
- Архивировать изменение (`openspec archive add-kind-internal-gateway`) после ревью.
