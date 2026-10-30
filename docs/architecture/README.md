# Архитектура сервисов

Модель [LikeC4](https://likec4.dev/) текущей архитектуры nomnom: сервисы, их связи и сценарии. Путь одного заказа словами — [разбор](../order-walkthrough.md). Решение — [ADR 44](../adr/0044-architecture-as-code-with-likec4.md).

```bash
make arch-dev
```

Цель открывает схемы в браузере и обновляет их при правке `.c4`. Проверка — `make arch-check`: синтаксис модели, совпадение с `ops/` и актуальность [каталога событий](../events-catalog.md).

| Файл | Содержание |
| --- | --- |
| [`specification.c4`](specification.c4) | Виды элементов и связей |
| [`model.c4`](model.c4) | Элементы и связи |
| [`views.c4`](views.c4) | Схемы и сценарии |

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

## Что в модель не входит

- Стратегия расширения [ADR 41](../adr/0041-cell-based-architecture-with-control-plane.md): вторая Cell и Market Migration не реализованы.
- CLI `orders-admin` и `restaurants-admin`, DLQ, служебные топики Kafka Connect, PgBouncer.
- Слайсы и use cases: они описаны в README контекстов.
- Размещение по неймспейсам: оно описано в [`ops/kind`](../../ops/kind/README.md).

## Как поддерживать

Модель пишется вручную и описывает то, что есть в коде и `ops/`. Изменение, которое добавляет процесс, хранилище, топик или синхронную связь между контекстами, правит и модель. Перед коммитом запускайте `make arch-check`.

[`ops/architecture`](../../ops/architecture/) сверяет с моделью topics из `topics.yaml`, бэкенды `GRPCRoute` и releases из `ops/kind/values`. Не сверяются: подписки consumer'ов в коде (объявлены на четырёх языках), хранилища, связи с SpiceDB и Temporal. Заголовок связи `-[kafka]->` — список коротких имён topics: из него каталог событий берёт потребителей.

Термины берутся из [Ubiquitous Language](../ubiquitous-language.md).
