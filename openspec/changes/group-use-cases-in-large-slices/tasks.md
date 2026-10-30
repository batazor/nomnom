# Tasks

## 1. Группировка

- [x] 1.1 `delivery/slices/delivery`: группы `offer`, `assignment`, `execution`, `tracking`, `duration`; импорты и пути в README (`FR-732`, `FR-733`, `AC-999`).
- [x] 1.2 `orders/slices/order`: группы `placement`, `kitchen`, `delivery`, `closure`, `lookup`; импорты, `.mockery.yml`, перегенерация моков (`FR-732`, `FR-733`, `AC-999`).
- [x] 1.3 `restaurants/slices/restaurantorder`: группы `intake`, `preparation`, `board`; импорты, `test/` (`FR-732`, `FR-733`, `AC-999`).

## 2. Документация

- [x] 2.1 ADR 6: уровень группы и его ограничения; ADR 3: одна схема на каждом уровне, глубина ограничена.
- [x] 2.2 Пути в `docs/testing.md`, `docs/security.md`, README сервисов, `boundaries/orders/docs/adr`.

## 3. Проверка

- [x] 3.1 `make check` в `delivery` и `orders`, `npm run check` и тесты `restaurants`; телеметрия и теги `.feature` без изменений (`AC-1000`); `openspec validate group-use-cases-in-large-slices --strict`.
