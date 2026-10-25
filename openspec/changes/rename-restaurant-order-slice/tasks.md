# Tasks

## 1. Переименование

- [ ] 1.1 Проверить `git status` в `boundaries/restaurants/slices/kitchenorder`: незакоммиченных правок других сессий нет.
- [ ] 1.2 `git mv slices/kitchenorder slices/restaurantorder`, `start-preparing-order` → `start-preparation`, `mark-order-ready` → `mark-ready-for-pickup`; переименовать экспортируемые типы и функции со старым корнем; обновить импорты в `di/`, `infrastructure/`, тестах; внешние строки (RPC, события, permissions, коллекции) не трогать (`FR-487`).

## 2. Документация и проверка

- [ ] 2.1 Обновить README use cases, `boundaries/restaurants/README.md`, `docs/kitchen.md` (убрать пункт об отложенном переименовании), `docs/ubiquitous-language.md` (убрать строку расхождения), `docs/testing.md`, `docs/specification-pattern.md`; проверить Markdown links.
- [ ] 2.2 Поиск старых имён по AC-552; `npm test`, typecheck, `npm run check`; `openspec validate rename-restaurant-order-slice --strict`.
