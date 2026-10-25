# 38. Миграции схемы применяет сам процесс при старте

Date: 2026-10-26

## Status

Accepted

Уточняет [8. Storage Strategy](0008-storage-strategy-postgresql-and-mongodb.md),
[17. Cloud Native Application Runtime](0017-cloud-native-application-runtime.md) и
[31. Outbox Storage](0031-outbox-storage-polling-relay-and-clients.md) (процессы
`<context>-migrate`).

## Context

Миграции каждого контекста запускала отдельная команда (`orders-migrate`, `payments-migrate`,
`delivery-migrate`, `restaurants-migrate`) init container'ом перед процессом. Это лишний
бинарь, лишняя конфигурация chart и отдельный шаг, порядок которого относительно остального
деплоя не очевиден: например, publication CDC (ADR 37) создаёт миграция, и коннектор может
стартовать до неё.

## Decision

- Миграции встроены в образ процесса. Каждый долгоживущий процесс контекста (API, consumer,
  worker, reconciler) при старте сначала приводит схему к встроенным миграциям и только потом
  поднимает остальное. Применённые миграции пропускаются: на актуальной базе шаг ничего не
  меняет.
- Одновременный старт нескольких pods сериализуется блокировкой хранилища (PostgreSQL —
  advisory lock goose или sqlx; MongoDB — идемпотентные операции и блокировка контекста).
- Ошибка миграции останавливает процесс до открытия портов (fail fast, ADR 17); readiness
  остаётся `503`, pod перезапускается.
- Отдельных команд `<context>-migrate`, init containers и values `migrate` в charts нет.

## Consequences

- Схема всегда соответствует коду запущенного процесса; деплой — один шаг.
- Время миграции входит в startup процесса: `startupProbe` должен его покрывать.
- Миграции остаются обратно совместимыми с предыдущей версией кода: во время rolling update
  старые pods работают с новой схемой.
- Регистрация коннектора CDC (ADR 37) — отдельный hook Job, он по-прежнему ждёт задачу
  коннектора в `RUNNING` и перезапускает её, если она стартовала раньше миграции.
