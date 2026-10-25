# Spec Delta

## ADDED Requirements

### Requirement: Открытие ресторана от имени пользователя
**ID:** FR-601

Resolvers `openRestaurant` и `closeRestaurant` SHALL требовать access token и вызывать `restaurants.OpenRestaurant`/`CloseRestaurant` через internal gateway с service token BFF и user assertion пользователя; право `restaurant#manage` MUST проверять `restaurants`. Отказ `NOT_FOUND` SHALL становиться GraphQL error `NOT_FOUND`, `PERMISSION_DENIED` — `PERMISSION_DENIED`, `INVALID_ARGUMENT` — `INVALID_ARGUMENT`, остальные сбои — `UNAVAILABLE` без внутренних деталей.

#### Scenario: Владелец открывает ресторан
**ID:** AC-751

- **WHEN** пользователь с валидным access token вызывает `openRestaurant` закрытого ресторана
- **THEN** `restaurants` получает вызов с user assertion этого пользователя, а payload содержит ресторан с `acceptingOrders = true` и `changed = true`

#### Scenario: Повтор не меняет состояние
**ID:** AC-752

- **WHEN** `closeRestaurant` вызывается для уже закрытого ресторана
- **THEN** ответ без GraphQL errors содержит `changed = false` и `acceptingOrders = false`

#### Scenario: Без access token
**ID:** AC-753

- **WHEN** `openRestaurant` или `closeRestaurant` вызывается без access token
- **THEN** ответ — error `UNAUTHENTICATED`, `restaurants` не вызывается

#### Scenario: Нет права управлять рестораном
**ID:** AC-754

- **WHEN** `restaurants` отвечает `PERMISSION_DENIED`
- **THEN** ответ — error `PERMISSION_DENIED` без деталей `restaurants`
