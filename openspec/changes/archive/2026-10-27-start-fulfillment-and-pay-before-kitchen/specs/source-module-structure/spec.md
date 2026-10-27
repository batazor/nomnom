# Spec Delta

## MODIFIED Requirements

### Requirement: Узкие порты доступа restaurants
**ID:** FR-548

Use cases `restaurants` SHALL зависеть от портов доступа и чтения, содержащих только нужные им операции; адаптер MUST NOT заглушать методы порта, которые ему не нужны.

#### Scenario: Адаптер consumer'а
**ID:** AC-638

- **WHEN** consumer `delivery.v1.delivery-events` проверяет право записать Pickup
- **THEN** его адаптер реализует только нужный ему порт доступа без заглушек
