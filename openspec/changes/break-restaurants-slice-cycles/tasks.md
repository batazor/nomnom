# Tasks

## 1. Разрыв циклов

- [ ] 1.1 `Money` в `restaurant/domains/restaurant/vo`, импорты слайсов и `test/` (`FR-728`, `AC-992`).
- [ ] 1.2 Убрать импорты портов `cook` из `restaurantorder`, совместимость проверяется при сборке cradle (`FR-728`, `AC-992`).
- [ ] 1.3 `listKitchenOrdersInStates` в `restaurantorder`, адаптер `simulator/infrastructure/pending-kitchen-orders.ts`, сборка в `simulator/di` (`FR-728`, `AC-992`, `AC-993`).

## 2. Проверка направлений

- [ ] 2.1 Правила направлений каждого слайса в `.dependency-cruiser.cjs` (`FR-729`, `AC-994`).

## 3. Документация

- [ ] 3.1 `boundaries/restaurants/README.md`: граф слайсов и новое место `Money`; ADR 6: строка `restaurants` в таблице проверок и правило ацикличности слайсов.

## 4. Проверка

- [ ] 4.1 `npm run check` и тесты `restaurants`; временная обратная связь отвергается `npm run architecture`, после чего убирается (`AC-994`); `openspec validate break-restaurants-slice-cycles --strict`.
