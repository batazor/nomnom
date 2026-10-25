# Proposal

## Why

nomnom нужен отдельный customer-facing web UI с едиными архитектурными границами, доступной компонентной основой и типобезопасным публичным API до начала реализации продуктовых экранов. Базовый scaffold сейчас позволит закрепить эти соглашения без преждевременного переноса бизнес-логики во frontend.

## What Changes

- Добавляется отдельная application boundary `boundaries/ui/` для customer-сценариев ресторанов, меню, корзины и заказов; courier, restaurant и operator UI остаются вне scope.
- Добавляется минимальный Vite/React/TypeScript scaffold: routing shell, providers, layout и один placeholder-экран без продуктовой логики.
- Подключаются Tailwind CSS 4, web-компоненты Astra и React Aria foundation; локальные адаптеры и nomnom-обёртки размещаются в `shared/ui/`.
- Добавляются семантические design tokens, light/dark theme, mobile-first и accessibility conventions.
- Добавляются GraphQL Code Generator с client preset и граница публичного GraphQL API WunderGraph Cosmo; frontend не получает доступа к gRPC, Kafka, Temporal, БД или адресам bounded contexts.
- Фиксируются правила feature colocation, i18n/`Intl`, server/local/form state, loading/errors, order-status presentation mapping, idempotency для Place Order, authorization UX и observability hygiene.
- Создаются `boundaries/ui/README.md`, `docs/ui.md`, корневая ссылка и Accepted ADR о frontend component foundation через ADR CLI.
- Добавляется пустой `boundaries/ui/ops/` с объяснением правила: deploy/runtime-артефакты принадлежат приложению, но не создаются до отдельного решения.
- SPA/SSR, окончательный GraphQL client и authentication/session остаются явно обозначенными open decisions; Storybook и browser OTel не становятся обязательными зависимостями этого шага.
- Pro-материалы Astra и React Native/Expo animations не используются.

## Capabilities

### New Capabilities

- `customer-web-application`: Архитектурный и исполняемый foundation отдельного customer web-приложения, его UI/accessibility conventions и публичная GraphQL-граница.

### Modified Capabilities

Нет.

## Impact

- Новые каталоги: `boundaries/ui/`, `boundaries/ui/ops/`, `openspec/changes/add-customer-ui-foundation/specs/customer-web-application/`.
- Новая общесистемная документация и ADR в `docs/`; ссылка в корневом `README.md`.
- Новые frontend-зависимости: React, Vite, TypeScript, Tailwind CSS 4, React Router, GraphQL Code Generator, GraphQL и выбранный provisional GraphQL client; Astra web packages используются только в рамках их MIT-лицензии.
- Публичный backend contract не меняется: UI потребляет существующую архитектурную границу Cosmo GraphQL, а реальная schema/operations и продуктовые экраны остаются вне scope.
- Расхождение с исходным предположением о vendoring будет документировано: актуальный официальный способ подключения web Astra — npm-пакеты `@astra-rn/react` и `@astra-rn/styles`; собственными являются адаптеры и обёртки в `shared/ui/`, а не исходники upstream package.
