# Register Restaurant

## Сценарий

- **Тип:** Command.
- **Actor:** Оператор платформы или владелец ресторана.
- **Намерение:** Создать ресторан как управляемый ресурс.
- **Результат:** Создан `Restaurant` с обязательным pickup `address` (непустая строка после trim); onboarding, verification и billing остаются вне сценария.

## Границы

- **Владелец:** `restaurants/slices/restaurant`.
- **Домен:** `domains/restaurant`.
- **Взаимодействие:** Authoritative authorization выполняется в `restaurants`.

## Acceptance

BDD-сценарии размещаются в `acceptance/register-restaurant.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

