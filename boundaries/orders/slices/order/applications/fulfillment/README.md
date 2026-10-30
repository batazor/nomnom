# Fulfillment port

Package не является use case. Он объявляет единственный application-порт Order Fulfillment Workflow, общий для use cases слайса `order`, и политику классификации входящих сигналов.

## Порты

| Порт | Кто использует | Семантика |
| --- | --- | --- |
| `Starter` | `startfulfillment` | Старт workflow одного `Order` с его `OrderPlaced` (`StartRequest{OrderID, EventID, OccurredAt}`) после commit Inbox; повтор адресует тот же workflow и дедуплицируется по `event_id`, закрытый workflow заново не стартует |
| `Notifier` | `confirmorder`, `rejectorder`, `record…` | Передача сигнала с исходным `event_id`; workflow дедуплицирует по нему; закрытый workflow — `ErrWorkflowNotFound` |
| `CancellationRequester` | `requestordercancellation` | Передача Cancellation Request с `RequestID` и исходным subject; бизнес-отказ workflow — `ErrCancellationRefused` |

`KindPreparationDelayed` (с `ExpectedReadyAt`) и `KindPreparationFailed` — не шаги FSM `Order`: задержка только заменяет срок готовности `Order` (Record Preparation Delay, `ExpectedReadyAt` уходит в уведомление), неудачу consumer передаёт workflow без изменения агрегата.

Порт остаётся в слайсе `order` как контракт его use cases; на Temporal-клиенте слайса [`fulfillment`](../../../fulfillment/README.md) его реализует composition root (`di/workflow.go`), и слайсы друг друга не импортируют. Выбор Signal, Update или `SignalWithStart`, Workflow ID `order:<order_id>` и имена сигналов — ответственность Temporal-адаптера [`slices/fulfillment/infrastructure/temporal`](../../../fulfillment/infrastructure/temporal/README.md); правила процесса (маршрут, сроки, допустимость отмены, компенсации) — доменной модели `slices/fulfillment/domains/fulfillment`.

## Поток входящего сигнала

Каждый из 9 входящих use cases (`confirmorder`, `rejectorder`, `record…`) пишет insert-first протокол Inbox сам, общего handler'а нет: они различаются тем, что читают, проверяют и сохраняют.

```text
unitofwork.AcceptOnce: UoW { Inbox.Accept(consumer, event_id) → load Order → domain method → ClassifySignal → Save } → commit → Notify(event_id)
```

Общая здесь только политика `ClassifySignal`:

| Результат доменного метода | Outcome | Сохраняется |
| --- | --- | --- |
| успех | `applied` | `Order` + Inbox |
| `AlreadyInState`, `TransitionAlreadyApplied` | `already_applied` | Inbox |
| `InvalidTransition` для `completed`/`cancelled` | `ignored_late` | Inbox |
| Inbox duplicate | `duplicate` | ничего |

Record Courier Assignment не двигает State: FSM только допускает сигнал (финальный `Order` — `ignored_late`), а назначение и `order#delivery` пишутся до commit в том же unit of work (dual-write rule `docs/security.md`), в том числе при reassignment. Изменение — `applied` и `Save`, его отсутствие — `already_applied` без `Save`.

Workflow уведомляется во всех успешных ветках, включая duplicate: это восстанавливает сбой между commit и уведомлением при повторной доставке. Ошибка уведомления возвращается вызывающему consumer, чтобы сообщение не было подтверждено. Решение и отличие от формулировки event-driven документации описаны в OpenSpec change `implement-orders-application-layer`.
