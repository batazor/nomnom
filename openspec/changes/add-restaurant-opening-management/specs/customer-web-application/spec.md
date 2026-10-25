# Spec Delta

## ADDED Requirements

### Requirement: Экран открытия ресторана владельцем
**ID:** FR-602

UI SHALL показывать на `/manage/restaurants/:restaurantId` состояние открытия ресторана и одно действие «Открыть ресторан» или «Закрыть ресторан», вызывающее `openRestaurant` или `closeRestaurant`. Пока команда выполняется, действие MUST быть в состоянии pending и не отправлять повторную команду; состояние SHALL меняться только по ответу команды, без optimistic update.

#### Scenario: Закрытие ресторана
**ID:** AC-755

- **WHEN** владелец открытого ресторана нажимает «Закрыть ресторан»
- **THEN** действие переходит в pending, повторное нажатие не отправляет вторую команду, а после ответа экран показывает «Закрыт» и действие «Открыть ресторан»

### Requirement: Отказы открытия ресторана в UI
**ID:** FR-603

UI SHALL показывать отказы `PermissionDenied`, `NotFound` и `Unauthenticated` команды открытия пользовательским сообщением без изменения состояния, а остальные ошибки — generic сообщением с ручным повтором. Автоматический retry MUST NOT выполняться.

#### Scenario: Нет права управлять рестораном
**ID:** AC-756

- **WHEN** `closeRestaurant` отвечает error с кодом `PermissionDenied`
- **THEN** UI показывает сообщение об отсутствии прав, а состояние ресторана не меняется

#### Scenario: Transport error
**ID:** AC-757

- **WHEN** команда не дошла до BFF
- **THEN** UI показывает generic сообщение, и повторное нажатие отправляет команду заново

## MODIFIED Requirements

### Requirement: Отдельное customer web-приложение
**ID:** FR-001

Система SHALL предоставлять отдельную application boundary `boundaries/ui/` для customer web-сценариев, не смешанную с доменной моделью backend bounded contexts или будущими courier, restaurant и operator интерфейсами. Исключение — экран открытия ресторана владельцем (FR-602, FR-603): до появления отдельного restaurant UI он MAY жить в `boundaries/ui/` изолированной feature под префиксом `/manage/` без ссылок из customer-навигации.

#### Scenario: Запуск базового приложения
**ID:** AC-004

- **WHEN** разработчик запускает customer web-приложение в development-режиме
- **THEN** открывается routing shell с одним локализованным placeholder-экраном без продуктовой бизнес-логики
