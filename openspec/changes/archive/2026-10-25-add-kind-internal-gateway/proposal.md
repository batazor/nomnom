# Proposal

## Why

Синхронные gRPC-вызовы между BFF и bounded contexts определены контрактами (ADR 7), но не
определено, как caller находит backend и как запрещён прямой доступ в обход общих
транспортных политик. Сетевые правила нельзя проверить без Kubernetes, а тестового
кластера в проекте нет.

## What Changes

- Тестовое окружение kind в `ops/kind/` с целями `kind-*` корневого `Makefile`; развёртывание
  описано в Skaffold (`ops/kind/skaffold/skaffold.yaml`): кластер
  из одного узла без default CNI, Cilium с Hubble, Envoy Gateway и Gateway API CRD,
  закреплённые в `ops/kind/versions.env`.
- Internal gateway `nomnom-gateway/internal` (Service `internal-gateway`, ClusterIP) и
  централизованный реестр `GRPCRoute` с транспортными политиками timeout и retries.
- Правило: все синхронные вызовы бизнес-API других приложений проходят через internal
  gateway; адрес gateway передаётся приложениям через `INTERNAL_GATEWAY_ADDR`.
- NetworkPolicy для прикладных неймспейсов и data plane gateway, запрещающие обход gateway
  и разрешающие только перечисленные инфраструктурные подключения.
- Автоматические проверки статусов gateway, A/B-изоляции прямых вызовов с вердиктами Hubble,
  неизвестных маршрутов, DNS и kubelet-проб под политиками; проверки, которым нужны бизнес-RPC,
  инфраструктура или BFF, выводятся как заблокированные.
- ADR 14–16 и документация.

## Capabilities

### New Capabilities

- `internal-service-communication`: маршрутизация синхронных вызовов бизнес-API через
  internal gateway, транспортная семантика gateway и сетевая изоляция от прямых вызовов.
- `kubernetes-test-environment`: воспроизводимое неинтерактивное kind-окружение для
  интеграционных тестов и E2E, которым нужен Kubernetes.

### Modified Capabilities

## Impact

- Новые каталоги и файлы: `ops/kind/` (Skaffold, cluster config, манифесты, values, скрипты
  проверок), корневой `Makefile`.
- Bounded contexts устанавливаются в kind существующими Helm charts (ADR 12) с values окружения;
  код приложений не меняется.
- Будущие gRPC-клиенты всех языков подключаются к `INTERNAL_GATEWAY_ADDR`, а каждый новый RPC
  требует записи в реестре маршрутов с решением о retries.
- Зависимости окружения: Docker, kind, Skaffold, kubectl, Helm, jq.
