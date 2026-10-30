## ADDED Requirements

### Requirement: Роли хранилища и шаг назначения delivery
**ID:** FR-711

Потребители хранилища `delivery` SHALL зависеть только от своей роли (транзакционная часть, read models, reconciliation, ревизии); Advance Assignment SHALL выполнять выбор кандидата и offer прикладными сервисами в одной транзакции; снятие назначения SHALL быть реализовано в одном месте.

#### Scenario: Шаг назначения одной транзакцией
**ID:** AC-970

- **WHEN** Activity Assignment Workflow выполняет Advance Assignment для ожидающей доставки
- **THEN** закрытие просроченного offer, выбор кандидата и новый offer выполняются в одной Unit of Work

## REMOVED Requirements

### Requirement: Роли хранилища и прикладные сервисы delivery
**ID:** FR-543

**Reason**: требование описывало Replan Delivery, который удаляется.

**Migration**: заменено FR-711: те же роли хранилища, шаг назначения Advance Assignment одной транзакцией вместо Replan; AC-633 заменён AC-970.
