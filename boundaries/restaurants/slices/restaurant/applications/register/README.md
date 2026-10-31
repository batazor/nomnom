# Register Restaurant

## Сценарий

- **Тип:** Command.
- **Actor:** Оператор платформы или владелец ресторана.
- **Намерение:** Создать ресторан как управляемый ресурс.
- **Результат:** Создан `Restaurant` в обязательном неизменяемом Market (`market_id`, slug, ADR 41); onboarding, verification и billing остаются вне сценария.

## Границы

- **Владелец:** `restaurants/slices/restaurant`.
- **Домен:** `domains/restaurant`.
- **Взаимодействие:** Authoritative authorization выполняется в `restaurants`.

## Acceptance

BDD-сценарии размещаются в `acceptance/register.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

