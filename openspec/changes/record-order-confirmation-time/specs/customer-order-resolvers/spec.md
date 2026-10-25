# Spec Delta

## ADDED Requirements

### Requirement: История заказа содержит подтверждение рестораном
**ID:** FR-495

Resolver `order(id)` SHALL переводить факт `restaurant_accepted` в milestone `CONFIRMED` с его моментом; без этого факта milestone `CONFIRMED` MUST NOT появляться.

#### Scenario: Подтверждённый заказ
**ID:** AC-564

- **WHEN** `orders` возвращает факт `restaurant_accepted` в `12:03`
- **THEN** `milestones` содержат `CONFIRMED` с `occurredAt = 12:03` между `PLACED` и последующими событиями
