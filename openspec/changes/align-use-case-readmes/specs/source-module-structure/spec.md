# Spec Delta

## ADDED Requirements

### Requirement: У каждого сценария есть README
**ID:** FR-727

Каталог в `applications/` с handler'ом самостоятельной операции SHALL содержать `README.md` с типом, actor, намерением, результатом, владельцем данных и существенными взаимодействиями. Общий код слайса (порты, ошибки, представления, общие шаги) MUST NOT считаться сценарием и не требует README.

#### Scenario: Сценарий без README
**ID:** AC-990

- **WHEN** читатель открывает `delivery/slices/delivery/applications/get_order_delivery`
- **THEN** README называет actor, намерение, результат и владельца данных

#### Scenario: Общий код слайса
**ID:** AC-991

- **WHEN** каталог содержит только ошибки слайса, например `applications/error`
- **THEN** README в нём не требуется
