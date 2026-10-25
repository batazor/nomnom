# Spec Delta

## Purpose

Определяет единый fixture-driven customer journey screen для визуальной и interaction-проверки основных этапов заказа до подключения продуктового API.

## ADDED Requirements

### Requirement: Единая визуализация customer journey

Система SHALL показывать на одном route связанные области выбора еды, оформления и активной доставки, чтобы пользователь мог понять полный customer flow без перехода между страницами.

#### Scenario: Просмотр комплексного экрана

- **WHEN** пользователь открывает customer UI
- **THEN** он одновременно видит restaurant/menu context, cart/checkout summary и active order с courier progress

### Requirement: Явные fixture data

Экран SHALL использовать только локальные typed fixtures и MUST NOT выполнять GraphQL-запросы, mutation, payment operation или вызовы внутренних backend endpoints.

#### Scenario: Работа preview без backend

- **WHEN** пользователь взаимодействует с комплексным экраном
- **THEN** отображаемые данные и изменения состояния остаются локальными и не создают network business request

### Requirement: Локальные presentation interactions

Экран SHALL позволять выбрать fixture menu item, изменить его количество и переключить демонстрационный шаг заказа без заявления production business semantics.

#### Scenario: Изменение preview-состояния

- **WHEN** пользователь активирует доступный control выбора, количества или статуса
- **THEN** связанные области экрана согласованно обновляются на основе fixture data

### Requirement: Доступный responsive preview

Комплексный экран SHALL сохранять keyboard navigation, visible focus, semantic labels, mobile usability, light/dark theme и reduced-motion behavior.

#### Scenario: Использование на узком экране с клавиатуры

- **WHEN** пользователь открывает экран с mobile viewport и использует только клавиатуру
- **THEN** области следуют логичному порядку, controls доступны, а horizontal overflow отсутствует

