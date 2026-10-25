# Spec Delta

## ADDED Requirements

### Requirement: BFF развёрнут в kind из воспроизводимого образа
**ID:** FR-502

Тестовое окружение SHALL разворачивать BFF из образа с закреплённой версией и checksum Cosmo Router; сборка MUST падать при несовпадении checksum. BFF SHALL вызывать контексты только через internal gateway.

#### Scenario: Подмена Router
**ID:** AC-574

- **WHEN** скачанный архив Router не совпадает с закреплённым SHA-256
- **THEN** сборка образа BFF завершается ошибкой

### Requirement: User identity проверяется насквозь
**ID:** FR-503

Проверка kind SHALL подтверждать цепочку ADR 32: запрос к BFF без access token отклоняется, запрос с access token тестового identity provider доходит до `orders` как `Subject{user}`, а тот же user assertion от другого workload отклоняется `UNAUTHENTICATED`.

#### Scenario: Отсутствующий заказ через BFF
**ID:** AC-575

- **WHEN** проверка запрашивает `order(id)` несуществующего заказа с валидным access token
- **THEN** ответ содержит `order = null` без ошибок, что подтверждает принятие assertion в `orders`

#### Scenario: Запрос без access token
**ID:** AC-576

- **WHEN** проверка запрашивает `order(id)` без заголовка `Authorization`
- **THEN** ответ содержит ошибку аутентификации
