# Proposal

## Why

ADR 6 помещает адаптер, обслуживающий один слайс, в `infrastructure/` этого слайса, а корневой gRPC-адаптер `restaurants` содержит RPC всех шести слайсов. `infrastructure/grpc/service.ts` импортирует 44 модуля слайсов, а изменение сценария часто затрагивает и слайс, и корень: `restaurants/infrastructure/grpc` менялся в 8 из 49 недавних коммитов со сценариями. Файлы RPC уже разбиты по темам, поэтому перенос почти механический.

## What Changes

- RPC и mapping переезжают в `slices/<slice>/infrastructure/grpc/`.
- `infrastructure/grpc/service.ts` только объединяет RPC слайсов в `RestaurantsService`.
- В корне остаются сервер, идентификация запроса, ошибки, трассировка, proto и generated code.
- ADR 6 уточняет размещение gRPC: один proto-сервис на контекст собирается в корне, его методы принадлежат слайсам.

Не входит:

- изменения proto, RPC, кодов ошибок и маршрутов;
- новые proto-сервисы по слайсам;
- перенос gRPC в других контекстах: в `orders` корневой адаптер импортирует всего 8 пакетов слайсов и выгоды не даст.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `source-module-structure`: RPC `restaurants` принадлежат слайсам.

## Impact

- `boundaries/restaurants/infrastructure/grpc/*`, `boundaries/restaurants/slices/*/infrastructure/grpc/`.
- Импорты в `boundaries/restaurants/test/`.
- `boundaries/restaurants/README.md`, ADR 6.
