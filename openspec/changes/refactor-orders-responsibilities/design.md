# Design

## Decisions

1. **Outbox.** `outbound.IntegrationMapper.Map(event) (message, occurredAt, error)`; таблица соответствия событий вместо switch внутри хранилища. `event_id` вычисляется mapper'ом по прежней формуле, чтобы повтор не дал второго события (ADR 13).
2. **CompletedAt.** `Order.Complete(completedAt)`; Activity CompleteOrder передаёт момент из Workflow (`workflow.Now` через вход Activity) или часы use case — выбирается тот источник, который детерминирован при повторе.
3. **Назначение курьера.** `order.CourierAssignment` с методами `Supersedes`, `Release`; use cases вызывают домен.
4. **Классификация ошибок.** `ErrorKind` (invalid input, not found, conflict, domain rule, unavailable, permission, internal); каждая таблица адаптера покрывает все виды, тест проверяет полноту.
5. **Роли хранилища.** Интерфейсы рядом с потребителями; PostgreSQL и memory реализуют их; Wire связывает один store со всеми ролями.
6. **Порты соседей.** Пакет `applications/neighbours` в слайсе fulfillment; адаптеры (`grpcclient`, `neighbours`) зависят от него, а не от `temporal`.
