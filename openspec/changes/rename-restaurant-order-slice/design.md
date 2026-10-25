# Design

## Context

См. `proposal.md`. Слайс `kitchenorder` содержит `domains/restaurantorder`, 14 use cases, `di/registrations.ts`, `infrastructure`; на него ссылаются `di/restaurants.ts`, gRPC-сервис, SpiceDB access control, реестр коллекций MongoDB и около десяти тестовых файлов. Параллельно идёт работа над Kitchen command RPCs (`add-kitchen-command-rpcs`) в тех же файлах.

## Decisions

### 1. Переименование через `git mv`

Каталоги переносятся `git mv`, чтобы история файлов сохранилась; затем правятся импорты. Строковые значения, которые попадают наружу (имена RPC, событий, коллекций, permissions SpiceDB), не меняются: это контракты и данные.

### 2. Имена use cases по языку

| Было | Стало |
| --- | --- |
| `start-preparing-order` | `start-preparation` |
| `mark-order-ready` | `mark-ready-for-pickup` |

Экспортируемые типы и функции с устаревшим корнем (`StartPreparingOrder…`, `MarkOrderReady…`) переименовываются в `StartPreparation…`, `MarkReadyForPickup…`. Остальные use cases уже названы по языку.

### 3. Порядок относительно других изменений

Change выполняется, когда в рабочем дереве нет незакоммиченных правок других сессий в `slices/kitchenorder`; иначе переименование их сломает. Исполнитель проверяет `git status` перед началом.

## Risks / Trade-offs

- [Конфликт с параллельной работой в тех же файлах] → проверка `git status` и выполнение одним коммитом.
- [Пропущенный импорт] → typecheck и полный набор тестов.

## Migration Plan

`git mv`, импорты, документы, проверки. Rollback — revert.
