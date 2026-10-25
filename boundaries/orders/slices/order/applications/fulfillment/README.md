# Fulfillment port

Package не является use case. Он объявляет единственный application-порт Order Fulfillment Workflow, общий для use cases слайса `order`, и общий поток отражения входящих бизнес-фактов.

## Порты

| Порт | Кто использует | Семантика |
| --- | --- | --- |
| `Starter` | `placeorder` | Старт workflow одного `Order` после commit; повтор адресует тот же workflow |
| `Notifier` | `confirmorder`, `rejectorder`, `record…` | Передача бизнес-факта с исходным `event_id`; workflow дедуплицирует по нему; закрытый workflow — `ErrWorkflowNotFound` |
| `CancellationRequester` | `requestordercancellation` | Передача Cancellation Request с `RequestID` и исходным subject; бизнес-отказ workflow — `ErrCancellationRefused` |

`KindPreparationDelayed` (с `ExpectedReadyAt`) и `KindPreparationFailed` — не Fulfillment Facts `Order`: задержка только заменяет срок готовности `Order` (Record Preparation Delay через `Reflector`, `InboundFact.ExpectedReadyAt` уходит в уведомление), неудачу consumer передаёт workflow без изменения агрегата.

Порт остаётся в слайсе `order` как контракт его use cases; реализует его слайс [`fulfillment`](../../../fulfillment/README.md), и `order` его не импортирует. Выбор Signal, Update или `SignalWithStart`, Workflow ID `order:<order_id>` и имена сигналов — ответственность Temporal-адаптера [`slices/fulfillment/infrastructure/temporal`](../../../fulfillment/infrastructure/temporal/README.md); правила процесса (маршрут, сроки, допустимость отмены, компенсации) — доменной модели `slices/fulfillment/domains/fulfillment`.

## Reflector

`Reflector` выполняет insert-first протокол Inbox для 8 входящих use cases:

```text
application.AcceptOnce: UoW { Inbox.Accept(consumer, event_id) → load Order → domain method → Save } → commit → Notify(event_id)
```

| Результат доменного метода | Outcome | Сохраняется |
| --- | --- | --- |
| успех | `applied` | `Order` + Inbox |
| `AlreadyInState`, `FactAlreadyRecorded` | `already_applied` | Inbox |
| `InvalidTransition` для `completed`/`cancelled` | `ignored_late` | Inbox |
| Inbox duplicate | `duplicate` | ничего |

`ReflectWith` добавляет шаг `InTransaction` внутри того же unit of work до `Save`: он выполняется для `applied` и `already_applied`, но не для `duplicate` и `ignored_late`, а его ошибка откатывает Inbox record; вернув `changed`, шаг требует сохранить `Order` и для `already_applied` (новый срок Pickup при reassignment). Так Record Courier Assignment пишет `order#delivery` до commit (dual-write rule `docs/security.md`), в том числе при reassignment, когда факт уже записан.

Workflow уведомляется во всех успешных ветках, включая duplicate: это восстанавливает сбой между commit и уведомлением при повторной доставке. Ошибка уведомления возвращается вызывающему consumer, чтобы сообщение не было подтверждено. Решение и отличие от формулировки event-driven документации описаны в OpenSpec change `implement-orders-application-layer`.
