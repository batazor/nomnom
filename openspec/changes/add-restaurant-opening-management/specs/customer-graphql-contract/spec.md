# Spec Delta

## ADDED Requirements

### Requirement: Mutations открытия ресторана в схеме
**ID:** FR-600

Публичная GraphQL-схема BFF SHALL предоставлять mutations `openRestaurant(restaurantId: ID!)` и `closeRestaurant(restaurantId: ID!)`, возвращающие `RestaurantOpeningPayload` с `restaurant` после команды и `changed`. Состояние открытия MUST передаваться полем `Restaurant.acceptingOrders`; mutations MUST NOT принимать идентификатор пользователя аргументом.

#### Scenario: Типы для экрана владельца
**ID:** AC-750

- **WHEN** UI запускает `npm run codegen` с operations `OpenRestaurant` и `CloseRestaurant`
- **THEN** генерируются typed documents с полями `changed` и `restaurant.acceptingOrders` без ошибок валидации
