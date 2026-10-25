# Design

## Decisions

### Проход reconciliation

1. **Graph → business.** `ReadRelationships(order#customer)` постранично (`page_size`). Для страницы — один запрос в PostgreSQL `id, customer_id` по `id = ANY(...)`. Relationship orphan, если заказа нет, и stale, если `customer_id` другой.
2. **Business → graph.** Заказы PostgreSQL постранично по `id`; заказ без relationship своего `customer_id` (по множеству, собранному в шаге 1) — `TOUCH`, ZedToken записывается в `authorization_revision`.

### Grace и двухфазное удаление

SpiceDB не хранит время записи relationship, а `TOUCH` оформляемого заказа предшествует commit. Reconciler держит в памяти кандидатов `tuple → first_seen`; удаляет кандидата, только если он orphan и `now - first_seen ≥ grace` (по умолчанию 10m; больше максимальной длительности транзакции оформления). Кандидат, переставший быть orphan, забывается. Рестарт процесса сбрасывает кандидатов и только откладывает удаление. Missing связь восстанавливается сразу: заказ уже закоммичен, `TOUCH` идемпотентен.

Удаление — `WriteRelationships(OPERATION_DELETE)` конкретной tuple; повтор успешен.

### Процесс

`cmd/orders-reconciler` — `lifecycle.Worker`: проход сразу после старта, затем каждые `interval` (5m). Ошибка прохода логируется и не останавливает процесс (следующий проход повторит). Каждый проход пишет log `checked`, `orphans_pending`, `orphans_deleted`, `missing_repaired` — drift metric до подключения OpenTelemetry. Требует `POSTGRES__DSN` и `SPICEDB__ENDPOINT`. Один экземпляр (`replicaCount: 1`): параллельные reconcilers безопасны (операции идемпотентны), но избыточны.
