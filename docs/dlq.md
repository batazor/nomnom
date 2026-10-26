# Разбор DLQ

Решение: [ADR 28. Selective Use of Watermill CQRS](adr/0028-selective-use-of-watermill-cqrs.md#восстановление-из-dlq).

Consumer отправляет в `<stream>.dlq` то, что повтором не лечится: битый record (deserialization), неизвестный `event_type` и permanent error handler'а. Это дефект кода или контракта, поэтому DLQ разбирает оператор после исправления, а не автоматический retry. Transient-ошибки в DLQ не попадают: partition ждёт, пока зависимость вернётся.

## Модель

- DLQ общий для всех consumer groups стрима. Каждая группа разбирает только свои dead letters — header `consumer_group_poisoned`.
- Что разобрано, хранит Kafka consumer group `<group>.dlq` (у `orders` — `orders.dlq`, у `restaurants` — `restaurants.dlq`): её committed offset на `<stream>.dlq` — первая неразобранная запись. Чужие dead letters группа проходит, не трогая. Lag `<group>.dlq` — верхняя оценка неразобранного backlog группы: в него входят и чужие записи.
- Записи разбираются по порядку в partition. Чтобы оставить запись в середине, сначала разбирают всё до неё.
- **Redrive** публикует dead letter обратно в исходный стрим: тот же ключ (`aggregate_id`), payload и envelope headers, без `reason_poisoned`, `topic_poisoned`, `consumer_group_poisoned`, `deserialization_error`, плюс header `redriven_from=<stream>.dlq/<partition>@<offset>`. Событие снова получают все группы стрима. Inbox делает повтор no-op там, где событие уже обработано. Если запись снова падает, она возвращается в DLQ с `redriven_from`.
- **Discard** помечает записи разобранными без публикации. Каждая запись и причина пишутся WARN-логом `dead letter discarded` — это журнал решения.
- Redrive доставляет событие позже более новых событий того же агрегата. Use cases уже различают поздние факты (`aggregate_version`, outcome `ignored late`), так что порядок не ломает состояние. Новый handler, который полагается на порядок, обязан проверять версию сам.

## Процедура

Сигнал — alert `nomnom-orders-kafka-poison` (`nomnom.messaging.poison.messages`) и лог `integration event sent to DLQ` с `error_class` и `detail`; у `restaurants-consumer` — та же метрика и WARN-лог `restaurants.order-placed dead-lettered …`.

Команда разбора у каждого контекста своя, контракт один (аргументы, вывод, offset `<group>.dlq`):

| Контекст | Pod | Команда | Стримы |
| --- | --- | --- | --- |
| `orders` | `-n nomnom-orders deploy/orders-consumer` | `/orders-admin` | `restaurants.v1.restaurant-order-events`, `delivery.v1.delivery-events` |
| `restaurants` | `-n nomnom-restaurants deploy/restaurants-consumer` | `node dist/cmd/restaurants-admin/main.js` (в dev-образе `make kind-dev` — `node cmd/restaurants-admin/main.ts`) | `orders.v1.order-events` |

Примеры ниже — для `orders`; для `restaurants` подставляются pod, команда и стрим из таблицы:

```bash
kubectl -n nomnom-restaurants exec deploy/restaurants-consumer -- node dist/cmd/restaurants-admin/main.js dlq list --stream orders.v1.order-events
```

1. Посмотреть неразобранные записи группы:

   ```bash
   kubectl -n nomnom-orders exec deploy/orders-consumer -- /orders-admin dlq list --stream restaurants.v1.restaurant-order-events
   ```

   Одна строка JSON на запись в stdout (логи команды `restaurants-admin` идут в stderr): `partition`, `offset`, `event_id`, `event_type`, `aggregate_id`, `occurred_at`, `reason`, `redriven_from`.

2. Классифицировать по `reason`:

   | Причина | Что сделать |
   | --- | --- |
   | `unknown event_type` — событие, которое группа должна читать | выкатить handler, затем redrive |
   | `unknown event_type` — событие не для этой группы | discard с причиной |
   | `deserialization_error` — consumer отстал от контракта | выкатить consumer с новым контрактом, затем redrive |
   | `deserialization_error` — publisher записал битый record | исправить publisher; событие не восстановить из DLQ, discard с причиной и ссылкой на инцидент |
   | permanent error handler'а — дефект mapping/handler'а | исправить, выкатить, затем redrive |
   | permanent error — данные события неверны | discard с причиной; исправление данных — отдельным решением владельца |

3. Redrive — всё неразобранное группы или одну partition до offset включительно:

   ```bash
   kubectl -n nomnom-orders exec deploy/orders-consumer -- /orders-admin dlq redrive --stream restaurants.v1.restaurant-order-events
   kubectl -n nomnom-orders exec deploy/orders-consumer -- /orders-admin dlq redrive --stream restaurants.v1.restaurant-order-events --partition 0 --through 41
   ```

4. Discard — одна partition до offset включительно, с причиной:

   ```bash
   kubectl -n nomnom-orders exec deploy/orders-consumer -- /orders-admin dlq discard --stream restaurants.v1.restaurant-order-events --partition 0 --through 41 --reason "INC-12: broken record of restaurants 1.4.2"
   ```

5. Повторить `dlq list`: список пуст или содержит только оставленное намеренно. Проверить, что redriven-события дошли: лог handler'а, состояние Order, отсутствие новой записи в DLQ с тем же `event_id`.

Команды идут разово в pod consumer'а: там уже есть конфигурация Kafka (`KAFKA__BROKERS`, `KAFKA__CONSUMER_GROUP`) и разрешённый egress. Прерванная команда продолжает с последней разобранной записи: offset `<group>.dlq` коммитится после каждой.

## Ограничения

- Retention `<stream>.dlq` должен быть больше окна разбора. В kind topics создаются при первой публикации с retention брокера. Для production retention DLQ задаётся явно при создании topic.
- Две команды разбора одной группы одновременно не запускаются: offset `<group>.dlq` не защищён от гонки.
