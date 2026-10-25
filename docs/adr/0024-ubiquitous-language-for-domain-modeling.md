# 24. Ubiquitous Language for Domain Modeling

Date: 2026-10-25

## Status

Accepted

## Context

BiteRoute разделён на bounded contexts `orders`, `restaurants`, `delivery` и `payments`, а BFF и runtime добавляют application/integration concepts. Существующие glossary, SDD, ADR, `.feature` и модели используют близкие, но не всегда одинаковые имена: например, `Kitchen Order` и `Restaurant Order`, domain `status` и `State`, presentation `stage` и orchestration `Stage`. Без нормативного языка такие расхождения проникают в контракты, события и observability, а одинаковое слово скрывает разные модели контекстов.

[ADR 6](0006-ddd-structure-inside-vertical-slices.md) запрещает общей модели заменять модели bounded contexts, [ADR 7](0007-grpc-contracts-and-buf-schema-registry.md) помещает Protobuf на infrastructure boundary, [ADR 21](0021-pure-fsm-transitions.md) отделяет aggregate state от Temporal orchestration, а [ADR 22](0022-specification-pattern-for-domain-policies.md) задаёт язык domain policies. Нужен нормативный словарь, связывающий эти решения и задающий управляемую эволюцию терминов.

## Decision

Принять [`docs/ubiquitous-language.md`](../ubiquitous-language.md) как обязательный Ubiquitous Language проекта. Словарь разделяется по bounded contexts и общесистемным integration/application областям; одинаковое написание между контекстами не создаёт общий domain type. Внутри контекста один термин имеет одно значение, новые синонимы запрещены.

Published Language передаётся только через Protobuf-контракты. Общими между контекстами являются только типизированные identifiers, `Money` и event envelope; context-specific event payload не становится общей моделью. Shared domain package не создаётся. Собственные BSR-модули остаются самодостаточными, а consumer явно переводит Transport DTO или Integration Event в свою Application DTO/Command/Query и Domain Model на application/infrastructure boundary. Импорт доменной модели соседнего контекста запрещён.

Имена следуют общей грамматике: aggregate/entity — существительное; Command — imperative `<Verb><Aggregate>`; Query — `Get<X>`/`List<X>`; Domain/Integration Event — past tense `<Aggregate><Fact>` с owner-контекстом publisher; FSM State — существительное/причастие, Trigger — глагол; Specification — eligibility-форма. `State` означает business state aggregate, `Stage` — orchestration state Workflow, `Status` — customer-facing presentation; `Phase` не используется.

Язык обязателен в SDD, ADR, Gherkin, Commands/Queries/Events, domain types, Protobuf, GraphQL и domain-oriented logs/observability. Новый или изменённый термин в Protobuf, GraphQL или SDD обновляет словарь в том же PR; изменение термина обновляет затронутую документацию и проходит совместимую migration. Неопределённая продуктовая семантика помечается как несогласованная, а не заполняется предположением.

## Consequences

Документация, код и контракты получают проверяемую общую грамматику, при этом модели контекстов остаются независимыми. Межконтекстные mappings становятся явными, а случайные shared domain abstractions и скрытые синонимы получают основание для отклонения на review.

Каждое изменение vocabulary теперь требует обновлять словарь и связанные источники. Уже существующие расхождения не переименовываются этим ADR автоматически: они зафиксированы как migration candidates в словаре и должны исправляться отдельными изменениями с учётом Protobuf/event compatibility, persisted data, Inbox retention и observability queries.
