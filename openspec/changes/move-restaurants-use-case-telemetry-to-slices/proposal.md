# Proposal

## Why

Список instrumented use cases `restaurants` лежит в корневом `di/usecases.ts`. Новый сценарий слайса требует правки в корне процесса, хотя handler регистрируется в `slices/<slice>/di/registrations.ts`, а забытая запись молча оставляет сценарий без телеметрии. В `orders` телеметрия подключается в `di` слайса, поэтому связанные правки лежат рядом.

## What Changes

- Каждый слайс экспортирует список своих use cases из `di/registrations.ts`, рядом с регистрацией handlers. Имена и типы (`command`, `query`) не меняются.
- `di/usecases.ts` оставляет только механизм `instrumentUseCases`.
- `di/restaurants.ts` и `di/consumer.ts` собирают списки слайсов для своего процесса.

Не входит:

- новые имена span и метрик, изменение `UseCaseTelemetry`;
- проверка, что телеметрия подключена к каждому handler'у;
- перенос самого декоратора в слайсы: процесс по-прежнему применяет его к cradle.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `source-module-structure`: список use cases телеметрии принадлежит слайсу.

## Impact

- `boundaries/restaurants/slices/*/di/registrations.ts`.
- `boundaries/restaurants/di/{usecases,restaurants,consumer}.ts`.
- `boundaries/restaurants/README.md`, `docs/observability.md`, если они называют расположение списка.
