# Create Menu

## Сценарий

- **Тип:** Command.
- **Actor:** Авторизованный управляющий ресторана.
- **Намерение:** Создать меню ресторана.
- **Результат:** Создано принадлежащее ресторану меню в Market ресторана (ADR 41); события меню несут этот `market_id`.

## Границы

- **Владелец:** `restaurants/slices/menu`.
- **Домен:** `domains/menu`.
- **Взаимодействие:** Проверяет право управления рестораном.

## Acceptance

BDD-сценарии размещаются в `acceptance/create-menu.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

