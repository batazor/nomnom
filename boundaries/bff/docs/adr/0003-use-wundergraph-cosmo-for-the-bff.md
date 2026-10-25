# 3. Use WunderGraph Cosmo for the BFF

Date: 2026-10-24

## Status

Accepted

Supercedes [2. BFF as the Client Entry Point](0002-bff-as-the-client-entry-point.md)

## Context

[ADR 2](0002-bff-as-the-client-entry-point.md) определил отдельный BFF как единую клиентскую точку входа, но предполагал собственный HTTPS/JSON REST API, OpenAPI и версию `/v1`. Для реализации выбран актуальный WunderGraph Cosmo. Прежний WunderGraph BFF SDK и `create-wundergraph-app` сняты с поддержки; текущий продукт строится вокруг GraphQL Federation и Cosmo Router.

Переход на Cosmo меняет внешний транспорт и способ описания контракта, но не меняет границы доменной ответственности: BFF не становится bounded context, не владеет доменными данными и обращается к сервисам-владельцам через их внутренние gRPC API.

## Decision

### Runtime и публичный API

Использовать WunderGraph Cosmo Router как публичный runtime BFF. Клиенты обращаются к GraphQL API Router через HTTPS. Публичный контракт задаётся GraphQL SDL и эволюционирует аддитивно; устаревающие поля помечаются `@deprecated`, а несовместимые изменения требуют отдельного миграционного решения. REST/OpenAPI-контракт и версия в URL `/v1`, предусмотренные ADR 2, не реализуются.

Edge ingress/gateway завершает внешний TLS, применяет rate limiting и базовую защиту от злоупотреблений. Router остаётся первой прикладной точкой. Платёжные webhooks, health checks, metrics и другие служебные интерфейсы не становятся GraphQL-операциями клиентского API.

### Модель развёртывания

На начальном этапе использовать один Cosmo Router и один локальный Go router plugin `bff`. Plugin является частью сервиса BFF и реализует клиентские GraphQL-сценарии через сгенерированный Cosmo gRPC-контракт. Router и plugin могут иметь несколько реплик и не хранят между запросами пользовательские сессии или состояние бизнес-операций.

Для локальной разработки graph компонуется из `graph.yaml` командой `wgc router compose`, а Router читает generated execution config из `config.json`. Production-интеграция с Cosmo control plane, registry и выдачей Router token определяется отдельно до развёртывания.

`wgc` закрепляется в `boundaries/bff/package.json`; generated plugin Protobuf, Go-код, mapping и lock-файл коммитятся. Локальный runtime toolchain устанавливается штатным механизмом `wgc`.

### Внутренние сервисы

Router plugin вызывает `orders`, `restaurants`, `delivery` и `payments` через их gRPC API согласно [общесистемному ADR 7](../../../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md). Внутренние клиенты будут генерироваться из неизменяемых BSR commits и использоваться инфраструктурными адаптерами клиентских сценариев.

Generated gRPC-контракт между Cosmo Router и plugin является техническим контрактом runtime и не заменяет gRPC-контракты bounded contexts. Клиенты не получают прямой доступ ни к одному из этих gRPC API.

### Ответственность и доверие

Cosmo Router и plugin совместно выполняют обязанности BFF: проверяют GraphQL-запрос, аутентификацию и общие ограничения сценария, формируют доверенный пользовательский контекст, координируют gRPC-вызовы и собирают клиентский результат. Конкретные настройки OIDC/JWT, allowlist операций и передача контекста будут реализованы отдельным изменением.

Семантическая валидация, бизнес-инварианты, права на доменное действие, принадлежность ресурсов и tenant-изоляция остаются в сервисе-владельце. BFF не читает чужие БД, не участвует в Kafka и не управляет Temporal workflow.

Ошибки внутренних сервисов преобразуются в безопасные GraphQL errors со стабильными публичными codes/extensions. Необработанные внутренние сообщения, stack traces, адреса и топология не раскрываются. Детальная политика ошибок, частичных GraphQL-ответов, идемпотентности mutations и cursor-based pagination определяется вместе с первыми доменными полями схемы.

### Организация кода

GraphQL SDL находится в `plugins/bff/src/schema.graphql`. Реализация plugin начинается в `plugins/bff/src`, а при появлении доменных сценариев разделяется на vertical slices по клиентским сценариям, например `checkout`, `order-tracking` и `restaurant-catalog`. Слоя `domains` у BFF нет.

## Consequences

Cosmo даёт единый GraphQL endpoint, composition, generated типобезопасный контракт Router/plugin и путь к operation governance, tracing и control plane. Локальная композиция позволяет разрабатывать и тестировать каркас без облачной учётной записи.

Отказ от REST/OpenAPI означает, что HTTP status mapping, RFC 9457 Problem Details и `/v1` из ADR 2 больше не являются публичным контрактом BFF. Клиентам потребуется GraphQL-клиент и правила обработки частичных данных и GraphQL errors. Эксплуатация включает Router и plugin, а production потребует решения о control plane, registry, токенах и политике опубликованных операций.

Инициализированный каркас содержит только технический запрос `bffInfo`. Доменные поля, внутренние gRPC-клиенты, аутентификация и production-конфигурация не реализованы.
