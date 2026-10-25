# Design

## Context

См. [proposal.md](proposal.md) — Why. Все четыре bounded contexts пока имеют только минимальный HTTP Hello World. README каждого контекста уже перечисляет Commands и Queries, а общесистемные ADR требуют Vertical Slice Architecture и DDD-структуру внутри slice. Языки и task runners различаются: Go (`orders`), TypeScript (`restaurants`), Rust (`delivery`) и Python (`payments`).

Temporal выбран только для Order Fulfillment Workflow в `orders`. Параллельный change `specify-order-fulfillment-orchestration` описывает целевой orchestration contract, но ещё не применён; поэтому этот change создаёт только SDK-aware skeleton без последовательности бизнес-шагов, таймаутов, compensation policy и event contracts.

## Goals / Non-Goals

**Goals:**

- сделать каждый документированный use case отдельным, легко обнаружимым vertical slice;
- обеспечить единый минимальный контракт `Input → Handler → Result/error` в идиомах языка контекста;
- гарантировать, что заглушка явно сообщает `not implemented`, а не имитирует успешное бизнес-поведение;
- добавить компилируемый Temporal workflow, Activities contract и worker composition root в `orders`;
- сохранить независимую сборку и проверки каждого bounded context.

**Non-Goals:**

- моделировать агрегаты, validation, authorization, transaction boundaries или repositories;
- выбирать поля будущих API request/response и Kafka payloads;
- запускать реальный Temporal Server или регистрировать worker в текущем deployment;
- реализовывать orchestration sequence, retries, timers, signals, updates, compensation и provider integrations;
- удалять существующие Hello World endpoints.

## Decisions

### Один каталог на один use case

Каждый сценарий получает собственный каталог верхнего уровня внутри исходного кода контекста и вложенную application boundary:

- Go: `internal/<slice>/applications/handler.go`;
- TypeScript: `src/<slice>/applications/handler.ts`;
- Rust: `src/<slice>/applications.rs`;
- Python: `app/<slice>/applications.py`.

Имена каталогов следуют идиомам языка: compact lower-case для Go, kebab-case для TypeScript, snake_case для Rust и Python. Каталоги `domains/` и `infrastructure/` пока не создаются: они появляются только когда конкретному slice потребуется доменная модель или адаптер.

Альтернатива с единым горизонтальным `handlers/` отклонена, потому что она нарушает принятую организацию по vertical slices. Генерация файлов на build-time также отклонена: каркас должен быть обычным читаемым исходным кодом, который можно развивать независимо.

### Минимальные типы без преждевременного контракта

Каждый slice экспортирует пустые или минимальные `Input` и `Result`/`Output` типы и stateless handler. Пустые типы намеренны: конкретные поля определяются будущей SDD-спецификацией use case, а не угадываются на этапе scaffolding.

Общий для bounded context `NotImplemented` error содержит каноническое имя use case. Handler принимает cancellation/context primitive языка, где он естественен, и всегда возвращает этот error. Такой контракт позволяет wiring и тестам компилироваться, но не создаёт ложного успешного результата.

Альтернатива с `panic`, `todo!()` или необработанным исключением отклонена: явная типизированная ошибка безопаснее для будущей композиции и проверяется тестом.

### Contract smoke tests вместо дублирования одинаковых тестов

У каждого context будет один parameterized/registry smoke test, который вызывает все его handlers и проверяет стабильный `not implemented` result с правильным use-case name. В Rust модульные тесты используют общий helper/trait; в остальных языках тест перечисляет exports явно. Явный список одновременно проверяет, что ни один документированный use case не пропущен.

### Temporal skeleton изолирован в `orders`

`orders` получает:

- `internal/orderfulfillment/applications/workflow.go` с сериализуемыми `WorkflowInput`, `WorkflowState` и workflow function;
- `internal/orderfulfillment/applications/activities.go` с Activities type и stub methods;
- константы workflow type и task queue, helper для Workflow ID `order:<order_id>`;
- `cmd/orders-worker/main.go` как отдельный composition root, регистрирующий workflow и Activities через официальный Go Temporal SDK.

Workflow возвращает типизированную `ErrNotImplemented` до выполнения внешних эффектов. Activities также не вызывают соседние contexts. Worker читает адрес/namespace из environment с безопасными development defaults, создаёт Temporal client, регистрирует skeleton и запускает SDK worker. Deployment и Helm не меняются, поэтому бинарник существует только как готовая точка дальнейшего wiring.

Temporal SDK добавляется как прямая Go dependency с версией, разрешённой `go get`; `go.mod` и `go.sum` фиксируют результат. Unit tests используют SDK test suite только там, где это нужно для проверки workflow registration/вызова без Temporal Server.

Альтернатива с собственными fake Temporal interfaces отклонена: она не проверяет сигнатуры официального SDK и отложила бы интеграционные ошибки до следующего change. Реальная orchestration logic отклонена как преждевременная до применения соответствующего SDD change.

### Existing entrypoints остаются неизменными по поведению

Hello World API каждого сервиса продолжает запускаться как прежде. Новые slices не подключаются к HTTP и не становятся публичными endpoints. `orders-worker` не добавляется в Dockerfile или Helm chart. Это делает изменение обратимо удалением новых каталогов и Temporal dependency.

## Risks / Trade-offs

- [Большое число механически похожих файлов создаёт шум] → сохранять по одному production-файлу на slice и один registry test на context; не создавать пустые domain/infrastructure слои.
- [Пустые Input/Result типы могут быть ошибочно приняты за готовый контракт] → каждый handler возвращает явный `not implemented`, а README/change отмечают scaffolding status.
- [Temporal SDK увеличивает Go dependency graph] → зависимость добавляется только в `orders`, фиксируется `go.sum` и проверяется `go test ./...`.
- [Параллельный orchestration change может уточнить сигнатуры] → skeleton содержит только устойчивые identity primitives и не кодирует порядок шагов; дальнейший apply меняет slice локально.
- [Worker с development defaults может быть случайно запущен без сервера] → ошибка подключения/запуска завершается явно; deployment не создаётся.

## Migration Plan

Миграция данных и rollout не нужны: внешнее поведение и deployment не меняются. Изменение применяется по одному bounded context с локальной проверкой, затем добавляется Temporal skeleton и выполняется общий набор сборок. Rollback состоит в удалении новых slice-каталогов, worker command и Temporal dependency.
