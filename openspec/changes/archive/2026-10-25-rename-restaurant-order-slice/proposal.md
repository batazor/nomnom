# Proposal

## Why

Слайс `restaurants/slices/kitchenorder` содержит агрегат `RestaurantOrder`, а по [GLOSSARY](../../../boundaries/restaurants/GLOSSARY.md) и [Ubiquitous Language](../../../docs/ubiquitous-language.md#restaurants) Kitchen Order — роль Restaurant Order на кухне после Acceptance, а не отдельная сущность. Названия use cases `start-preparing-order` и `mark-order-ready` тоже расходятся с терминами Start Preparation и Mark Ready for Pickup. Переименование уже записано как отложенное в [docs/kitchen.md](../../../docs/kitchen.md) («отдельным изменением вместе с кодом») и в таблице расхождений Ubiquitous Language.

## What Changes

- `slices/kitchenorder` → `slices/restaurantorder`.
- `applications/start-preparing-order` → `applications/start-preparation`, `applications/mark-order-ready` → `applications/mark-ready-for-pickup`, включая имена handlers и экспортов, где они повторяют старые названия.
- Обновляются импорты (`di/`, `infrastructure/grpc`, `infrastructure/spicedb`, `infrastructure/mongodb`, тесты), README use cases, `boundaries/restaurants/README.md`, `docs/kitchen.md`, `docs/ubiquitous-language.md`, `docs/testing.md`, `docs/specification-pattern.md`.
- Расхождение удаляется из списков отложенных изменений.

Не входит: изменение поведения, gRPC-контрактов, имён RPC и событий, коллекций MongoDB (их имена — данные, а не код).

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `restaurant-order-lifecycle`: код Restaurant Order находится в слайсе, названном по Ubiquitous Language.

## Impact

- `boundaries/restaurants/` (переименование каталогов и импортов), документы в `docs/`.
- Внешние контракты не меняются.
