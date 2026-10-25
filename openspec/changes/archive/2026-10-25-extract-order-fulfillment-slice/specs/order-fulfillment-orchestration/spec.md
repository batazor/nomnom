# Spec Delta

## ADDED Requirements

### Requirement: Решения процесса принимает доменная модель
**ID:** FR-483

Правила Order Fulfillment — следующий шаг маршрута, реакция на факты соседей, сроки, replan и эскалация, допустимость Cancellation Request и план компенсации — SHALL реализовываться доменной моделью слайса `fulfillment`, которая MUST NOT зависеть от Temporal SDK, часов и случайности. Temporal workflow SHALL только переводить Signals и Updates в вызовы модели и исполнять её решения.

#### Scenario: Replan проверяется без Temporal
**ID:** AC-546

- **WHEN** unit-тест модели применяет к процессу с `MaxReplans = 2` три истечения срока назначения курьера
- **THEN** модель дважды возвращает шаг `ReplanDelivery`, а на третий раз переводит процесс в ожидание решения оператора

#### Scenario: Компенсация по журналу шагов
**ID:** AC-547

- **WHEN** процесс с подтверждёнными Authorization и запросом в ресторан получает отказ ресторана
- **THEN** модель возвращает план компенсации из отмены Authorization и закрытия заказа как отказа, без отмены доставки, которая не создавалась

### Requirement: Внешние имена процесса не меняются при выделении слайса
**ID:** FR-484

Выделение слайса `fulfillment` MUST сохранять имена workflow, Signals, Update, Query, Activities, Task Queue, Search Attribute `OrderFulfillmentStage` и формат Input/Result.

#### Scenario: Сквозной сценарий после переноса
**ID:** AC-548

- **WHEN** тест адаптера на Temporal test environment проходит заказ от `OrderPlaced` до завершения
- **THEN** вызываются те же Activities с теми же именами и idempotency key шагов, что до переноса, а результат — `completed`
