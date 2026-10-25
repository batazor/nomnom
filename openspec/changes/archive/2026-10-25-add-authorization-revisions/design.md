# Design

## Decisions

- **Тип:** revision — непрозрачная строка (ZedToken). Агрегат её не интерпретирует. Это техническое поле snapshot (`authorizationRevision` / `authorization_revision`), как колонка `orders.authorization_revision`. Старые snapshots загружаются без revision.
- **Writers:** `touchOwner`, `touchStaff`, `deleteStaff` (restaurants) и `register_courier`, `assign_courier`, `unassign_courier` (delivery) возвращают revision записи. Use case сохраняет её в ресурсе в той же операции save, после записи в SpiceDB (порядок «запись до commit» не меняется). Repeat без записи revision не меняет.
- **Revision после `DELETE`:** revision после `DELETE` тоже сохраняется. Следующая проверка видит отзыв и не разрешает доступ по устаревшему снимку.
- **Чтение:** SpiceDB-адаптер получает порт `RevisionReader(resource type, id)` поверх репозитория. Для ресурсов с revision consistency `at_least_as_fresh(revision)`, иначе `minimize_latency`. Ошибка чтения revision — authorization unavailable (fail closed).
- **Reconciler:** `TOUCH` missing relationship сохраняет новую revision в ресурс. Удаление orphan revision не меняет, потому что ресурса нет или связь stale.
- **Доставка (`delivery:<id>`):** для `CancelDelivery` и Reassign сохраняется revision `DELETE`; для Accept — revision `TOUCH`.
- **Проверка:** unit-тесты: use case сохраняет revision; адаптер передаёт `at_least_as_fresh` с сохранённым токеном и `minimize_latency` без него. Integration против SpiceDB проверяет `minimize_latency` адаптера (без принудительной full consistency в тестовом клиенте): сразу после `AssignStaff` персонал получает `operate_kitchen`, сразу после `RevokeStaff` теряет его, и так же для назначения и переназначения курьера.
