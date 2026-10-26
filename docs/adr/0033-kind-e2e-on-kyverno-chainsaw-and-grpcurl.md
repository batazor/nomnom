# 33. Kind E2E on Kyverno Chainsaw and grpcurl

Date: 2026-10-25

## Status

Accepted

Уточняет [14. Использовать kind для тестового окружения Kubernetes](0014-использовать-kind-для-тестового-окружения-kubernetes.md)

## Context

Проверки kind-окружения написаны на bash (`ops/kind/scripts/verify.sh`): gRPC-вызовы
собираются вручную (кадр protobuf через `printf`, статус разбирается из headers и trailers
через `sed`), состояние ресурсов читается `kubectl` с `jsonpath`, повторяющаяся логика
расползается по вспомогательным функциям. Такой код хрупок, плохо читается и не проверяет
контракты: запрос не валидируется по proto, ответ — по типу.

Рассмотренные варианты:

- собственные Go-тесты на `sigs.k8s.io/e2e-framework` — гибко, но это собственный код
  клиентов, probe-образа и утверждений;
- [Kyverno Chainsaw](https://kyverno.io/docs/subprojects/kyverno-chainsaw) — декларативные
  тесты в YAML (apply, assert, error, wait, podLogs, proxy, catch), подпроект Kyverno
  (CNCF), используется в OpenTelemetry Operator и Crossplane;
- Testkube — платформа оркестрации тестов, избыточна для одного стенда;
- k6 — по [ADR 26](0026-k6-for-performance-testing.md) не заменяет kind E2E;
- Tracetest — проект фактически остановлен (облачная версия закрыта, последний релиз Core —
  октябрь 2024).

## Decision

Kind E2E пишутся как тесты Kyverno Chainsaw в `ops/kind/e2e/` по областям (`gateway/`,
`netpol/`, `calls/`, `infra/`, `bff/`); bash-проверок (`verify.sh`) больше нет:

- состояние ресурсов проверяется операциями `assert`/`error` с JMESPath, без `kubectl` и
  `jsonpath`; кластерные инварианты (deployments Available, нет рестартов и проваленных проб
  после политик) — `assert` без ресурса по спискам `x_k8s_list`;
- вызовы идут из pod fixtures (`e2e/chart/templates/probes.yaml`, образ
  `ops/kind/e2e/probe`) в нужных неймспейсах, под их NetworkPolicy: `net-probe` в неймспейсах
  приложений и в `nomnom-outsider`, `grpc-caller` в `nomnom-bff` с service identity.
  Клиенты настоящие: бизнес-API — `grpcurl` с descriptor sets всех контрактов из buf
  workspace и `google/rpc` из googleapis (ответ сверяется по `code`, `message` и reason
  `google.rpc.ErrorInfo`), сеть — `nc` и `nslookup`, GraphQL BFF — `curl`;
- повторяющийся шаг — `StepTemplate` (`templates/`: `grpc-call`, `tcp-connect`,
  `dns-lookup`, `netpol-applied`, `driver-call`, `customer-graphql`), параметры — `scenarios` и bindings, а не
  функции в shell. Шаг `script` — одна команда, утверждения — в `check`;
- тесты не повторяют то, что доказывает деплой (readiness workloads в Skaffold status check,
  `Accepted`/`ResolvedRefs` маршрутов в hook), и не проверяют статический рендер charts;
- тесты под NetworkPolicy применяют политики сами (`netpol-applied`, политики остаются);
  что drop вызван политикой, а не неработающим backend'ом, доказывают условия в том же тесте
  (backend Available, имя резолвится, gateway отвечает), а не прогон без политик, меняющий
  весь кластер. Политики — kustomization
  `manifests/netpol` (`kubectl apply -k`): базовые политики без namespace размножаются по
  неймспейсам приложений overlays, а операция Chainsaw `apply` помещает ресурс без namespace
  только в неймспейс теста. Временные fixtures (ingress к BFF) применяются `apply` и
  удаляются Chainsaw в конце теста;
- диагностика при ошибке — `catch` тестов (pod logs, events, describe, Hubble DROPPED);
  полный снимок окружения по-прежнему собирает `make kind-diagnostics`;
- версия Chainsaw закрепляется в `ops/kind/versions.env` и запускается через `go run` без
  установки; `make kind-test` и `make kind-e2e` запускают только Chainsaw.

Новые проверки сразу пишутся в Chainsaw.

## Consequences

Тесты описывают ожидаемое поведение, а не способ его получить; запросы валидируются по proto.
Повторяющаяся логика живёт в шаблонах шагов, а не в вспомогательных функциях. Появляется
собственный образ проверок, который пересобирается при изменении proto. Шаг `script`
по-прежнему исполняет одну команду в pod (`kubectl exec … grpcurl`), но логики в нём нет.
Шаги `script` не повторяются до успеха, поэтому ожидание асинхронных эффектов (отправка
spans, применение политик Cilium) — явный `sleep`. Outputs операций видны только внутри
шага: замеры «до и после» (retries) собраны в один шаблон шага. Вызов BFF идёт не через
`kubectl port-forward`, а из `nomnom-outsider` через временно открытый ingress: проверка
не держит фоновых процессов. Проверки инфраструктуры, которой нет в окружении, не выводятся
как заблокированные: ожидание выражено условием (Alloy доступен, только если развёрнут).
