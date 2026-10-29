# Report Delivery Failure

## Сценарий

- **Тип:** Command.
- **Actor:** Назначенный курьер или авторизованный диспетчер.
- **Намерение:** Зафиксировать невозможность завершить доставку.
- **Результат:** Неудача сохранена с кодом причины из каталога Delivery Failure Reason и необязательным пояснением; результат доступен `orders`.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** Записывает бизнес-факт через Outbox; retryability причины определяется SDD.

## Последовательность

```mermaid
sequenceDiagram
    actor Actor as Курьер / диспетчер
    participant App as Use case
    participant Delivery
    participant Store as Unit of Work / Outbox
    participant Kafka
    Actor->>App: сообщить результат доставки
    App->>Delivery: проверить actor и выполнить переход
    App->>Store: сохранить состояние и бизнес-факт
    Store-->>App: commit
    Store->>Kafka: опубликовать через Outbox
    App-->>Actor: результат
```

## Реализация

- **Handler:** `mod.rs` — `Input` (`reason` — код каталога `customer_unreachable`, `address_not_found`, `no_access`, `customer_refused`, `order_damaged`, `courier_incident`, `other`; `detail` — необязательный свободный текст, не длиннее 500 символов), `Output`, `Handler::execute(subject, input)`; требования FR-317. Неизвестный или пустой код и слишком длинное пояснение — `InvalidRequest`, а не `other`.
- **Порты:** `DeliveryAuthorization` (`ReportDeliveryFailure` над `Courier(id)` для курьера или `Delivery(id)` для диспетчера), `UnitOfWork`, `DeliveryRepository`, `CourierAssignment` (порт слайса `courier`), `DeliveryOutbox`, `DeliveryRelationshipWriter`, `AssignmentProcess` (после commit), `CandidateWake` (порт слайса `courier`, после commit).
- **Транзакция:** UoW: `Delivery` (`failed` с причиной) + освобождение `Courier` и DELETE `delivery#courier` до commit (`release_assignment`) + Outbox (`DeliveryFailed`).
- **Relationship:** курьер теряет `delivery#act_as_courier` и `order#view` при отчёте курьера и диспетчера (AC-691); revision `DELETE` сохраняется (FR-447); неудачный `DELETE` откатывает UoW.
- **Авторизация диспетчера:** `Delivery(id)` для `ReportDeliveryFailure` проверяется как `platform:nomnom#dispatch_delivery` (`operator`, AC-694), а не `delivery#act_as_courier`.
- **Наблюдаемость:** после commit — `nomnom.delivery.failed{nomnom.reason=<код>}`, одна JSON-запись `delivery.failure_reported` в stdout (код, `detail`, `delivery_id`, `order_id`, `courier_id`, `trace_id`/`span_id` при активном span) и атрибуты активного span `nomnom.delivery.failure.reason`/`nomnom.delivery.failure.detail`; `detail` в атрибуты метрик не попадает.
- **Допущение:** Код причины не классифицируется как retryable — решение остаётся за `orders`; неудача фиксируется только для `assigned`/`pickedUp`, курьер освобождается.

## Acceptance

BDD-сценарии размещаются в `acceptance/report_delivery_failure.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).

