# Design

## Decisions

1. `OPERATION_KINDS: Mapping[OperationKind, OperationKindRules]` в домене; `_apply_success`/отказ берут правило из таблицы; тест проверяет, что у каждого члена `OperationKind` есть правило, а отсутствие правила — исключение.
2. Метрики классифицируют отказы по той же таблице, без `isinstance`.
3. Перенос gRPC-модулей сохраняет публичные имена в `service.py` через реэкспорт только там, где их импортируют entrypoints или тесты; DI берёт генератор из `infrastructure/ids.py`.
4. Протоколы Outbox объявлены рядом с relay; Postgres-реализация реализует все.
