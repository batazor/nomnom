# customer-web-application Specification

## Purpose
Определяет проверяемые границы и базовое поведение отдельного customer web-приложения BiteRoute до реализации продуктовых экранов и бизнес-логики.

## Requirements

### Requirement: Отдельное customer web-приложение
**ID:** FR-001

Система SHALL предоставлять отдельную application boundary `boundaries/ui/` для customer web-сценариев, не смешанную с доменной моделью backend bounded contexts или будущими courier, restaurant и operator интерфейсами.

#### Scenario: Запуск базового приложения
**ID:** AC-004

- **WHEN** разработчик запускает customer web-приложение в development-режиме
- **THEN** открывается routing shell с одним локализованным placeholder-экраном без продуктовой бизнес-логики

### Requirement: Единая публичная API-граница
**ID:** FR-002

Customer web-приложение SHALL обращаться к backend только через публичный GraphQL API BFF и MUST NOT знать адреса bounded contexts, gRPC, Kafka, Temporal, баз данных или внутреннюю topology.

#### Scenario: Проверка frontend-конфигурации
**ID:** AC-005

- **WHEN** проверяются runtime-конфигурация и исходный код customer web-приложения
- **THEN** единственным business API endpoint является публичный GraphQL endpoint BFF

### Requirement: Типобезопасный GraphQL-контракт
**ID:** FR-003

GraphQL operations SHALL храниться рядом с владеющей ими feature, а используемые приложением TypeScript-типы и документы SHALL генерироваться из публичной GraphQL schema, а не поддерживаться вручную.

#### Scenario: Генерация типов из тестовой schema
**ID:** AC-006

- **WHEN** разработчик запускает code generation для валидной schema и colocated operation
- **THEN** генератор создаёт типизированный документ без ручного дублирования transport-типов

### Requirement: Доступный и responsive foundation
**ID:** FR-004

Интерактивные элементы SHALL сохранять keyboard navigation, visible focus, semantic labels и screen-reader semantics; базовый shell SHALL быть mobile-first и учитывать `prefers-reduced-motion`.

#### Scenario: Использование shell с клавиатуры на узком экране
**ID:** AC-007

- **WHEN** пользователь открывает placeholder-экран с mobile viewport и перемещается только клавиатурой
- **THEN** содержимое не требует горизонтальной прокрутки, интерактивные элементы доступны в логичном порядке, а фокус видим

### Requirement: Семантическая тема
**ID:** FR-005

Интерфейс SHALL выражать цвета, типографику, spacing, radius, shadow и motion через семантические design tokens и SHALL поддерживать light и dark theme без component-local палитр.

#### Scenario: Переключение системной цветовой схемы
**ID:** AC-008

- **WHEN** предпочтение пользователя меняется между light и dark color scheme
- **THEN** shell использует соответствующие значения семантических tokens с читаемым контрастом

### Requirement: Локализуемый пользовательский текст
**ID:** FR-006

Видимый пользователю текст SHALL проходить через i18n-слой, а валюты, даты и числа SHALL форматироваться через `Intl` с locale из конфигурации.

#### Scenario: Отображение placeholder на настроенной locale
**ID:** AC-009

- **WHEN** приложение запускается с поддерживаемой locale
- **THEN** placeholder-текст берётся из locale messages, а форматтеры используют ту же locale

### Requirement: Безопасное отображение ошибок и telemetry
**ID:** FR-007

UI SHALL показывать validation и business errors пользовательским языком, infrastructure errors — через generic recovery UI, и MUST NOT раскрывать внутренние gRPC, Kafka или Temporal детали. Telemetry MUST NOT содержать PII, payment details или полные GraphQL variables.

#### Scenario: Ошибка публичного GraphQL-запроса
**ID:** AC-010

- **WHEN** публичный GraphQL API возвращает infrastructure error с внутренними деталями
- **THEN** пользователь видит generic recovery message, а error report не содержит запрещённые данные или полные variables

### Requirement: Authorization остаётся backend boundary
**ID:** FR-008

Frontend SHALL использовать доступные actions из публичного API для UX, но MUST NOT считать скрытие элемента управления проверкой authorization; защищённая операция должна оставаться авторизованной backend-владельцем.

#### Scenario: Недоступное действие
**ID:** AC-011

- **WHEN** публичный API сообщает, что действие недоступно пользователю
- **THEN** UI не предлагает действие и не содержит client-side предположения, способное заменить backend authorization

### Requirement: Каталог, корзина и оформление заказа
**ID:** FR-501

Customer UI SHALL показывать открытые рестораны на `/`, меню ресторана с корзиной на `/restaurants/:restaurantId` и оформлять заказ через `placeOrder` с idempotency key на попытку, переиспользуемым при ручном retry; после оформления SHALL переходить на `/orders/:orderId`. Fixture-preview MUST быть удалён.

#### Scenario: Оформление из корзины
**ID:** AC-571

- **WHEN** покупатель добавляет доступные блюда в корзину, вводит адрес и оформляет заказ
- **THEN** UI отправляет `placeOrder` и открывает экран оформленного заказа

#### Scenario: Позиция закончилась при оформлении
**ID:** AC-572

- **WHEN** `placeOrder` возвращает problem `ORDER_ITEM_UNAVAILABLE` для позиции корзины
- **THEN** UI показывает отказ у этой позиции и не очищает корзину

#### Scenario: Недоступное блюдо не добавляется
**ID:** AC-573

- **WHEN** в меню есть позиция с `available = false`
- **THEN** она показана, но действие добавления в корзину недоступно
