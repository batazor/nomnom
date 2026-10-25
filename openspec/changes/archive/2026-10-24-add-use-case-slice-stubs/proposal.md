# Proposal

## Why

Use cases четырёх bounded contexts уже описаны как будущие vertical slices, но кодовая база содержит только Hello World-каркасы. Нужен единообразный компилируемый skeleton, чтобы следующие SDD changes могли добавлять поведение в заранее обозначенные границы, не смешивая application flow, domain model и infrastructure.

## What Changes

- Добавить в `orders`, `restaurants`, `delivery` и `payments` каталоги vertical slices для всех Commands и Queries, перечисленных в README соответствующего bounded context.
- Для каждого слайса добавить минимальные типизированные input/result и application handler/use case stub с явным результатом `not implemented`, без транспорта, persistence и бизнес-логики.
- Добавить в `orders` отдельный каркас Temporal: сериализуемые workflow inputs/signals/state, Order Fulfillment Workflow stub, Activity interfaces/stubs и composition-root worker command.
- Добавить smoke/unit tests, проверяющие импортируемость, вызов заглушки и отсутствие случайно реализованного поведения.
- Обновить build manifests только необходимыми зависимостями Temporal SDK и тестирования; сохранить существующие Hello World entrypoints рабочими.
- Не добавлять gRPC/GraphQL contracts, Kafka event names/payloads, БД/repositories, provider clients, Helm resources или реальные доменные переходы.

## Capabilities

### New Capabilities

Нет. Изменение создаёт внутренний каркас будущей реализации и не добавляет внешнего системного поведения.

### Modified Capabilities

Нет.

## Impact

- `boundaries/orders`: Go packages use-case slices, Temporal workflow/activity/worker skeleton, tests и Temporal SDK dependency.
- `boundaries/restaurants`: TypeScript use-case slices и tests без новых runtime dependencies.
- `boundaries/delivery`: Rust use-case modules и tests без новых runtime dependencies.
- `boundaries/payments`: Python use-case modules и tests без новых runtime dependencies.
- Сборка и проверки каждого bounded context должны продолжать проходить независимо; публичные transport contracts и runtime deployment не изменяются.
