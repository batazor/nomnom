# Proposal

## Why

Текущий placeholder подтверждает frontend foundation, но не показывает, как customer-сценарии nomnom складываются в цельный продукт. Один комплексный fixture-driven экран даст быструю визуализацию restaurant discovery, оформления и доставки без преждевременного создания набора маршрутов и backend-интеграции.

## What Changes

- Placeholder заменяется комплексным customer journey preview на одном route.
- Экран объединяет restaurant/menu selection, компактную корзину и checkout summary, а также активный заказ с courier progress.
- Все данные поставляются typed fixtures; GraphQL-вызовы, mutations, authentication, payment processing, карта и настоящая бизнес-логика не добавляются.
- Добавляются локальные presentation interactions для выбора блюда, количества и шага заказа; они демонстрируют состояния, но не объявляются production domain rules.
- Экран остаётся mobile-first, доступным с клавиатуры, локализованным, light/dark и совместимым с `prefers-reduced-motion`.

## Capabilities

### New Capabilities

- `customer-journey-preview`: Единый fixture-driven экран для визуальной проверки restaurant, checkout и active delivery customer experience.

### Modified Capabilities

Нет.

## Impact

- Изменяется UI route в `boundaries/ui/src/app/` и добавляется feature-owned preview в `boundaries/ui/src/features/`.
- Расширяются локализованные messages, semantic UI façade и component tests.
- Публичный GraphQL contract, BFF, bounded contexts и deploy artifacts не меняются.
- Новые runtime dependencies не требуются.
