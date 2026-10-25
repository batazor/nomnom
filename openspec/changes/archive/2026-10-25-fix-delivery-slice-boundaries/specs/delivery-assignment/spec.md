# Spec Delta

## ADDED Requirements

### Requirement: Занятость курьера меняется через слайс courier
**ID:** FR-485

Use cases слайса `delivery` SHALL проверять и менять занятость курьера только через прикладной порт слайса `courier` в текущей Unit of Work и MUST NOT вызывать методы агрегата `Courier` или `CourierRepository` напрямую. Атомарность FR-310 SHALL сохраняться.

#### Scenario: Принятие offer занимает курьера
**ID:** AC-549

- **WHEN** курьер принимает pending offer
- **THEN** курьер становится busy через порт `courier`, а `Delivery`, offer и `Courier` фиксируются одной транзакцией, как требует FR-310

#### Scenario: Сбой commit не оставляет курьера занятым
**ID:** AC-550

- **WHEN** commit транзакции принятия offer не удался
- **THEN** курьер остаётся available, offer pending, `Delivery` waiting

### Requirement: Слайсы delivery без циклических зависимостей
**ID:** FR-486

Слайс `courier` MUST NOT импортировать модули слайса `delivery`; зависимость допустима только в направлении `delivery → courier`.

#### Scenario: Проверка импортов
**ID:** AC-551

- **WHEN** проверка ищет `slices::delivery` в коде `slices/courier`
- **THEN** совпадений нет
