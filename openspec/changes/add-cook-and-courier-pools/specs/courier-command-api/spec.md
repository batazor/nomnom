# Spec Delta

## MODIFIED Requirements

### Requirement: Регистрация курьера — действие оператора
**ID:** FR-581

`RegisterCourier` MUST требовать `platform:nomnom#manage_couriers` (relation `operator` или `dispatcher`), принимать `courier_id` и `name` и выполнять `TOUCH courier#self` нового курьера до сохранения.

#### Scenario: Регистрация оператором
**ID:** AC-732

- **WHEN** operator регистрирует курьера для пользователя с именем
- **THEN** ответ содержит `courier_id`, а пользователь получает `courier#act` на этого курьера

#### Scenario: Регистрация не оператором
**ID:** AC-733

- **WHEN** пользователь без relation `operator` и `dispatcher` вызывает `RegisterCourier`
- **THEN** ответ `PERMISSION_DENIED`, курьер не создан
