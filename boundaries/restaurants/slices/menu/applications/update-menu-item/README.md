# Update Menu Item

## Сценарий

- **Тип:** Command.
- **Actor:** Авторизованный управляющий ресторана.
- **Намерение:** Изменить основные данные блюда.
- **Результат:** `Menu Item` обновлён в пределах инвариантов меню.

## Границы

- **Владелец:** `restaurants/slices/menu`.
- **Домен:** `domains/menu`.
- **Взаимодействие:** Не изменяет snapshot уже оформленного `Order`.

## Acceptance

BDD-сценарии размещаются в `acceptance/update-menu-item.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

