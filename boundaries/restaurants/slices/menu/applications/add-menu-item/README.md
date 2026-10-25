# Add Menu Item

## Сценарий

- **Тип:** Command.
- **Actor:** Авторизованный управляющий ресторана.
- **Намерение:** Добавить блюдо в меню.
- **Результат:** Создан `Menu Item` с исходными данными, ценой и доступностью.

## Границы

- **Владелец:** `restaurants/slices/menu`.
- **Домен:** `domains/menu`.
- **Взаимодействие:** Применяет инварианты `Menu`; бизнес-факт публикуется только при наличии требования.

## Acceptance

BDD-сценарии размещаются в `acceptance/add-menu-item.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

