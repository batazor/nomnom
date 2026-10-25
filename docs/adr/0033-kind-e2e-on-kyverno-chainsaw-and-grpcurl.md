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

Kind E2E пишутся как тесты Kyverno Chainsaw в `ops/kind/e2e/` по областям (`gateway/`, …):

- состояние ресурсов проверяется операциями `assert` с JMESPath, без `kubectl` и `jsonpath`;
- бизнес-API вызывается настоящим gRPC-клиентом — `grpcurl` с descriptor sets всех
  контрактов из buf workspace — изнутри неймспейса приложения (fixture `grpc-caller`, образ
  `ops/kind/e2e/probe`), под его NetworkPolicy и с его service identity. Ответ проверяется
  по `code` и `message`;
- повторяющийся шаг — `StepTemplate` (`templates/grpc-call.yaml`), параметры —
  `scenarios` и bindings, а не функции в shell;
- версия Chainsaw закрепляется в `ops/kind/versions.env` и запускается через `go run` без
  установки; `make kind-test` и `make kind-e2e` запускают его вместе с ещё не перенесёнными
  проверками `verify.sh`.

Проверки переносятся из `verify.sh` по секциям; новые проверки сразу пишутся в Chainsaw.

## Consequences

Тесты описывают ожидаемое поведение, а не способ его получить; запросы валидируются по proto.
Повторяющаяся логика живёт в шаблонах шагов, а не в вспомогательных функциях. Появляется
собственный образ проверок, который пересобирается при изменении proto. Шаг `script`
по-прежнему исполняет одну команду в pod (`kubectl exec … grpcurl`), но логики в нём нет.
Детали ошибок (`google.rpc.ErrorInfo`) grpcurl пока не декодирует: в descriptor sets нет
googleapis.
