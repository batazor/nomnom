# Spec Delta

## ADDED Requirements

### Requirement: Роли хранилища и прикладные сервисы delivery
**ID:** FR-543

Потребители хранилища `delivery` SHALL зависеть только от своей роли (транзакционная часть, read models, reconciliation, ревизии); Replan Delivery SHALL выполнять шаги назначения прикладными сервисами под одной проверкой прав и в одной транзакции; снятие назначения SHALL быть реализовано в одном месте.

#### Scenario: Replan одной проверкой
**ID:** AC-633

- **WHEN** выполняется Replan Delivery
- **THEN** авторизация проверяется один раз (`ReplanDelivery`), а снятие, выбор кандидата и offer выполняются в одной Unit of Work
