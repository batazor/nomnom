# Design

## Что куда переезжает

| Сейчас | Куда |
| --- | --- |
| `menu-commands.ts`, чтение меню из `service.ts` | `slices/menu/infrastructure/grpc/` |
| `restaurant-commands.ts`, чтение и администрирование ресторанов из `service.ts` | `slices/restaurant/infrastructure/grpc/` |
| `kitchen.ts`, `kitchen-board.ts`, Receive, Cancel, Queue и Record Pickup из `service.ts` | `slices/restaurantorder/infrastructure/grpc/` |
| `schedule-slots.ts` | `slices/scheduleslot/infrastructure/grpc/` |
| `cook-pool.ts` | `slices/cook/infrastructure/grpc/` |
| `simulator.ts` | `slices/simulator/infrastructure/grpc/` |
| Функции `messages.ts` | Mapping слайса-владельца типа; общий mapping остаётся в корне |

В корне остаются `server.ts`, `worker.ts`, `service.ts`, `errors.ts`, `revision.ts`, `subject.ts`, `service-token.ts`, `user-assertion.ts`, `jwks.ts`, `tracing.ts`, `client-telemetry.ts`, `buf.gen.yaml`, `nomnom/`, `gen/`.

## Контракт между слайсом и корнем

Каждый файл RPC сегодня берёт зависимости через `Pick<RestaurantsServiceDependencies, ...>` из `service.ts`. Если оставить так, слайс будет импортировать файл, который импортирует его самого. Поэтому:

- слайс объявляет свой тип зависимостей, например `MenuRpcDependencies`, и фабрику `createMenuRpcs`;
- `service.ts` определяет `RestaurantsServiceDependencies` как пересечение типов слайсов;
- слайс MUST NOT импортировать `infrastructure/grpc/service.ts`.

Сигнатура `createRestaurantsService(dependencies)` и плоский объект зависимостей не меняются, поэтому `di/restaurants.ts` и тесты сервиса остаются прежними. Тип `ServiceImpl<typeof RestaurantsService>` продолжает проверять, что реализованы все методы.

## Зависимости между слайсами

Mapping не должен создавать новых связей вне графа из `break-restaurants-slice-cycles`. У экспортируемых функций `messages.ts` есть один слайс-владелец: например, `toDeliveryWindowMessage` используют только RPC `scheduleslot`, поэтому она переезжает туда. Внутренний helper, нужный слайсам без разрешённого направления между ними, остаётся в корневом `messages.ts`, а тот не импортирует слайсы. Если таких helper'ов нет, файл удаляется.

Этот change применяется после `break-restaurants-slice-cycles`: правила направлений тогда сразу проверяют перенесённые адаптеры. Правило `generated-code-in-adapters` уже разрешает generated code в `slices/*/infrastructure`.

## Что не должно измениться

Проверка поведения — существующие тесты `grpc-*-integration`, `identity`, `tracing`, `observability`. Новых тестов перенос не требует. `npm run proto:check` подтверждает, что proto не изменился.
