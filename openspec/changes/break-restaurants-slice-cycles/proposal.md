# Proposal

## Why

Слайсы `restaurants` зависят друг от друга в обе стороны: `menu` ↔ `restaurant`, `restaurantorder` ↔ `cook` и `restaurantorder` ↔ `simulator`. Такой слайс нельзя понять или изменить отдельно от соседа, а зависимость не видна из структуры. В `orders` и `delivery` связи односторонние и проверяются линтером или тестом, а `.dependency-cruiser.cjs` в `restaurants` разрешает импорт между любыми слайсами.

## What Changes

- Value object `Money` переезжает из `menu` в `restaurant`: остаётся только `menu → restaurant`.
- `restaurantorder` перестаёт импортировать порты `cook`: `DatabaseKitchenBoard` и `RepositoryRestaurantExistence` больше не объявляют `implements CookWorkload` и `implements CookRestaurants`, совместимость проверяется при сборке в `di`.
- `restaurantorder` перестаёт импортировать `simulator`: выборку заказов по состояниям объявляет `restaurantorder`, а `simulator` передаёт ей свои `SIMULATED_STATES` через собственный адаптер.
- `.dependency-cruiser.cjs` фиксирует разрешённые направления для каждого слайса:

  ```text
  restaurant       → нет зависимостей от слайсов
  menu             → restaurant
  scheduleslot     → restaurant
  restaurantorder  → restaurant, menu
  cook             → restaurantorder
  simulator        → restaurantorder, restaurant
  ```

Не входит:

- изменение поведения, gRPC API, событий и схемы MongoDB;
- фасады слайсов: сосед по-прежнему импортирует домены и порты напрямую, это явная зависимость;
- разрыв циклов в других сервисах: в `orders` и `delivery` их нет, в `payments` и `controlplane` по одному слайсу.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `source-module-structure`: граф слайсов `restaurants` ацикличен и проверяется автоматически.

## Impact

- `boundaries/restaurants/slices/{menu,restaurant,restaurantorder,cook,simulator}`, импорты `Money` в `test/`.
- `boundaries/restaurants/.dependency-cruiser.cjs`.
- `boundaries/restaurants/README.md`, ADR 6.
