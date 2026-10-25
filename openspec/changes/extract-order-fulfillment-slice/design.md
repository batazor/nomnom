# Design

## Context

`slices/order/infrastructure/temporal/fulfillorder`: `workflow.go` (тип `run` с журналом Saga, фактами, сроками, `drive`, `step`, `expire`, `stage`, `compensate`), `signals.go` (handlers фактов, решения оператора, Cancellation Request с validator), `policy.go`, `contract.go` (Input, Fact, Outcome, Result, имена), `activities.go`, `client.go`, `searchattribute.go`, `register.go` и тесты на `testsuite`. Правила процесса нормативно описаны в `openspec/specs/order-fulfillment-orchestration` и [ADR 5 orders](../../../boundaries/orders/docs/adr/0005-order-fulfillment-orchestration.md).

## Goals / Non-Goals

**Goals:** правила процесса в доменной модели без Temporal; Temporal — адаптер; поведение и внешние имена без изменений.

**Non-Goals:** новые правила, другой оркестратор, versioning истории workflow.

## Decisions

### 1. Слайс `fulfillment`, а не пакет внутри `order`

По ADR 6 слайс — модуль бизнес-возможности с собственной моделью. У процесса своя модель (состояние процесса, Policy, Stage, Outcome), которая не является частью инвариантов `Order`. Слайс `order` остаётся владельцем агрегата и use cases, отражающих факты в `Order`.

```text
slices/
  order/                      агрегат Order, use cases, порт fulfillment
  fulfillment/
    domains/fulfillment/      Process, Policy, Stage, Outcome, Decision
      fulfillment.go          Process и конструктор
      facts.go                применение фактов и решений оператора
      route.go                следующий шаг маршрута
      deadlines.go            сроки, replan, эскалация
      compensation.go         план компенсации
      stage.go                Stage и его вычисление
      policy.go
    infrastructure/temporal/  workflow, signals, activities, client, search attribute
```

Зависимости: `fulfillment/infrastructure` → `fulfillment/domains`, `order/applications/fulfillment` (порт, который он реализует) и `order/applications/{completeorder,failorcancelorder}` (Activities). `order` не импортирует `fulfillment`. `fulfillment/domains` не импортирует ничего, кроме стандартной библиотеки.

### 2. Модель — состояние плюс чистые решения

`Process` — сериализуемая структура (её хранит workflow state). Операции возвращают решение, а не выполняют побочные эффекты:

- `Apply(fact Fact, at time.Time) Effect` — факт соседа (принят, отказ, начато, задержка, отказ кухни, готов, назначен, забран, доставлен, неудача доставки); `Effect` сообщает, нужен ли повтор шага (например, `ReplanDelivery`) и изменилось ли состояние;
- `Decide(OperatorDecision, at)` и `RequestCancellation(CancellationRequest, at) error` — с теми же отказами, что сейчас у validator Update;
- `Next() Step` — следующая Activity маршрута или ожидание;
- `NextDeadline() time.Time` и `Expire(now)` — сроки и их истечение;
- `Stage() Stage`, `Compensation() []Step`, `Outcome()`.

Workflow хранит `Process`, вызывает эти методы и превращает `Step` в Activity с прежними именами и idempotency key шага. Детерминизм обеспечивается тем, что модель не читает часы и не генерирует случайности: время передаёт workflow (`workflow.Now`).

### 3. Перенос тестов

Сценарии `workflow_test.go` делятся: правила (порядок шагов, replan до `MaxReplans`, эскалация, компенсации, поздние события, отказ отмены после Pickup) — в unit-тесты модели на таблицах; в адаптере остаются тесты интеграции с `testsuite` (Signals доходят до модели, Activities вызываются по решениям, Search Attribute публикуется, имена совпадают) и по одному сквозному сценарию на завершение, отмену и компенсацию.

### 4. Совместимость

Имена workflow, Signals, Update, Query, Activities, Task Queue, Search Attribute и формат Input/Result сохраняются, поэтому клиенты (`order` use cases, consumer) не меняются. Изменение кода workflow несовместимо с replay ранее начатых историй; production-развёртываний нет, а в kind перед выкаткой достаточно дождаться завершения или прервать тестовые workflow — это фиксируется в README.

## Risks / Trade-offs

- [Поведение изменится при переносе] → сначала переносятся тесты-сценарии как спецификация модели, потом код; сквозные тесты адаптера остаются зелёными на каждом шаге.
- [Модель станет анемичной обёрткой над switch] → решения формулируются в терминах процесса (Effect, Step, Stage), а не Temporal.

## Migration Plan

1. Модель и её тесты в `slices/fulfillment/domains/fulfillment` (копия правил из workflow).
2. Workflow и signals переводятся на модель; тесты адаптера сокращаются.
3. Пакет переезжает в `slices/fulfillment/infrastructure/temporal`; DI, wire, README.
4. Rollback: revert коммита; внешние контракты не менялись.
