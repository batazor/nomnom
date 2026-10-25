# Tasks

## 1. BFF

- [x] 1.1 Перегенерировать клиент `restaurants` (`make proto-generate`); mutations `openRestaurant`/`closeRestaurant` и `RestaurantOpeningPayload` в схеме, `npm run generate` (`FR-600`, `AC-750`).
- [x] 1.2 Слайс `restaurantmanagement` (applications, infrastructure на bufconn с проверкой service token и user assertion), resolvers, presenter, wiring; тесты resolvers (`FR-601`, `AC-751`–`AC-754`); README.

## 2. UI

- [x] 2.1 Feature `manage-restaurant` на `/manage/restaurants/:restaurantId`: состояние, действие с pending, отказы, ручной повтор; i18n ru/en; codegen; MSW-моки состояния открытия с ошибками в форме Cosmo; тесты, axe и 390px (`FR-602`, `FR-603`, `AC-755`–`AC-757`); README и `docs/ui.md`.

## 3. Проверка

- [x] 3.1 `npm run check` в `boundaries/bff`; `npm run check` и `npm run build` в `boundaries/ui`; `openspec validate add-restaurant-opening-management --strict`.
- [ ] 3.2 Сквозная проверка через kind с access token владельца (после выбора способа входа в браузере).
