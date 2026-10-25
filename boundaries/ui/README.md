# Customer UI

Customer web-приложение BiteRoute находится в отдельной application boundary `boundaries/ui/`. Оно отвечает за будущие пользовательские сценарии поиска ресторанов, меню, корзины и заказов, но не владеет backend-доменной моделью и не обращается к внутренним сервисам напрямую.

Поверх foundation добавлен один комплексный customer journey preview: restaurant/menu selection, checkout summary и активная доставка с customer-facing courier context одновременно видны на route `/`. Он нужен для визуальной проверки связности будущего продукта, а не задаёт production navigation.

Preview полностью работает на typed fixtures и локальном presentation state. Он не выполняет GraphQL business operations, payment, authentication, geolocation или courier worker actions; показанные totals, адрес, order stage и courier являются демонстрационными данными, а backend contract остаётся source of truth.

Preview намеренно не дробится механически на под-компоненты. Когда в BFF schema появятся соответствующие GraphQL operations, он заменяется production features по сценариям — `browse-restaurants`, `cart`/`place-order` и `view-order` — каждая со своими operations рядом с кодом, а `customer-journey-preview` удаляется.

## Запуск

Требуется Node.js 22 или новее.

```bash
cd boundaries/ui
npm ci
cp .env.example .env.local
npm run dev
```

Для локального mock transport в `.env.local` используется `VITE_ENABLE_MOCKS=true`. Единственный business endpoint приложения — `VITE_PUBLIC_GRAPHQL_URL`, адрес публичного GraphQL API BFF.

Основные проверки:

```bash
npm run format
npm run lint
npm run typecheck
npm test
npm run codegen
npm run build
npm run check
```

## Стек и структура

- React 19, TypeScript 7 и Vite 8;
- Tailwind CSS 4;
- React Aria Components как accessibility foundation;
- бесплатные web packages Astra `@astra-rn/react` и `@astra-rn/styles`;
- React Router;
- provisional urql client;
- GraphQL Code Generator с `client` preset;
- Vitest, Testing Library, axe-core и MSW.

Версии закреплены точно в [`package.json`](package.json) и [`package-lock.json`](package-lock.json). Biome используется для lint и format, как в других TypeScript-приложениях репозитория.

```text
src/
  app/                 bootstrap, providers, router и layout
  features/            feature-owned GraphQL operations и будущие UI slices
  shared/
    config/            validated public runtime config
    graphql/           client, generated types и mocks
    i18n/              typed messages и Intl formatters
    tokens/            semantic CSS tokens
    ui/                Astra façade и nomnom wrappers
  test/                общий test setup
ops/                   будущие runtime/deployment artifacts
```

Feature-specific code размещается внутри feature. Глобальные `components/`, `hooks/`, `services/` и `utils/` не создаются; код переносится в `shared/` только после реального повторного использования.

## GraphQL boundary

Допустимый runtime-путь: `browser → edge → Cosmo Router → BFF plugin → internal gateway → bounded context`. Решение о WunderGraph Cosmo закреплено в [локальном ADR BFF](../bff/docs/adr/0003-use-wundergraph-cosmo-for-the-bff.md).

UI не знает адреса backend boundaries и не импортирует gRPC, Kafka, Temporal или database clients. GraphQL operations хранятся рядом с feature, а TypeScript documents/types генерируются из публичной BFF schema:

```bash
npm run codegen
GRAPHQL_SCHEMA=https://example.test/graphql npm run codegen
```

Локальный default schema source — `../bff/plugins/bff/src/schema.graphql`. Generated files не редактируются вручную.

## Astra ownership

Актуальный web distribution Astra package-based: `@astra-rn/react` и `@astra-rn/styles` версии 1.0.1 публикуются с MIT metadata. Это отличается от исходного предположения о vendoring исходников. Pro и React Native/Expo animations не используются.

Feature и `app/` импортируют только façade из `src/shared/ui/`. Для обновления Astra нужно:

1. проверить latest-версии и license metadata через npm registry;
2. обновить exact pins и lockfile;
3. проверить API локальных wrappers;
4. выполнить `npm run check` и `npm run build`;
5. выполнить visual и accessibility regression check light/dark и mobile/desktop.

Exit strategy: заменить реализацию wrappers на React Aria Components, сохранив imports потребителей из `shared/ui/`. Второй component framework не добавляется.

Подробные conventions и open decisions описаны в [архитектуре UI](../../docs/ui.md).
