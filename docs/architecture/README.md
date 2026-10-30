# Архитектура сервисов

Модель [LikeC4](https://likec4.dev/) текущей архитектуры nomnom: сервисы, их связи и сценарии. Решение — [ADR 44](../adr/0044-architecture-as-code-with-likec4.md).

```bash
make arch-dev
```

Цель открывает схемы в браузере и обновляет их при правке `.c4`. Проверка модели — `make arch-validate`.

| Файл | Содержание |
| --- | --- |
| [`specification.c4`](specification.c4) | Виды элементов, связей и узлов развёртывания |
| [`model.c4`](model.c4) | Элементы и связи |
| [`views.c4`](views.c4) | Схемы и сценарии |
| [`deployment.c4`](deployment.c4) | Неймспейсы kind |

## Схемы

| Схема | Что показывает |
| --- | --- |
| `index` | Клиенты, глобальный контур и Cell |
| `cell` | Контексты, internal gateway и брокер внутри Cell |
| `orders`, `restaurants`, `delivery`, `payments` | Процессы и хранилища контекста |
| `messaging` | Кто пишет Outbox и кто читает топики |
| `controlplane` | Реестр и публикация маршрутов |
| `placeOrder` | Place Order и основной маршрут Order Fulfillment |
| `integrationEvent` | Outbox → Debezium → Kafka → Inbox |
| `subscription` | GraphQL-подписка через Cosmo Streams |
| `login` | Вход через Dex и проверка прав |
| `publishRoutes` | Изменение реестра Market |
| `kindNamespaces` | Размещение по неймспейсам kind |

## Что в модель не входит

- Стратегия расширения [ADR 41](../adr/0041-cell-based-architecture-with-control-plane.md): вторая Cell и Market Migration не реализованы.
- CLI `orders-admin` и `restaurants-admin`, DLQ, служебные топики Kafka Connect, PgBouncer.
- Слайсы и use cases: они описаны в README контекстов.

## Как поддерживать

Модель пишется вручную и описывает то, что есть в коде и `ops/`. Изменение, которое добавляет процесс, хранилище, топик или синхронную связь между контекстами, правит и модель. Автоматической сверки со стендом нет, запускайте `make arch-validate` перед коммитом.

Термины берутся из [Ubiquitous Language](../ubiquitous-language.md).
