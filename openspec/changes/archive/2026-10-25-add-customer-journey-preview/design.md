# Design

## Context

См. `proposal.md` и `specs/customer-journey-preview/spec.md`. В `boundaries/ui/` уже есть один route, Astra façade, semantic tokens, typed i18n, responsive shell и тестовая инфраструктура. Текущий placeholder не содержит product structure; новая визуализация должна остаться локальным prototype и не нарушить публичную GraphQL boundary.

## Goals / Non-Goals

**Goals:**

- показать цельную customer story на одном экране с правдоподобной информационной плотностью;
- сделать restaurant/menu, checkout и delivery status визуально различимыми, но связанными одной fixture-моделью;
- проверить будущие shared patterns: item selection, quantity control, order summary, progress и courier card;
- сохранить один route, accessibility и responsive foundation.

**Non-Goals:**

- production navigation и отдельные route каждого сценария;
- настоящие цены/налоги/fees, availability, checkout validation или order FSM;
- GraphQL integration, payment, geolocation, карта, authentication и authorization;
- courier worker interface: показывается только customer-facing courier context.

## Decisions

### 1. Один triptych screen вместо набора страниц

Desktop использует композицию из основной restaurant/menu области и правой journey rail, где checkout summary переходит в active delivery. Mobile складывает области в последовательность `restaurant → cart/checkout → active order`. Это быстрее демонстрирует продуктовую связность и не создаёт фиктивную route architecture.

Альтернатива — отдельные mock routes. Отклонена для preview: она увеличивает объём, но не улучшает сравнение состояний на одном viewport.

### 2. Feature-owned typed fixture model

Новый `features/customer-journey-preview/` владеет types, fixtures, presentation component и tests. Fixture содержит один ресторан, короткое меню, выбранные позиции, адрес, totals, courier и набор пользовательских delivery stages. Денежные значения хранятся в minor units или строковом точном представлении и форматируются через существующий `Intl` layer.

Fixture не экспортируется как domain model и не повторяет backend transport types. Альтернатива — разместить данные в `shared/`; отклонена из-за одного feature owner.

### 3. Локальный reducer только для демонстрации

Выбор позиции, quantity и активный presentation stage управляются локально внутри preview feature. Reducer не валидирует production invariants и не отправляет mutations. Derived subtotal вычисляется только для согласованности fixture view.

### 4. Спокойная consumer composition

Экран использует существующие tokens и Astra façade: compact header, сильную restaurant identity, плоские menu rows, один выделенный checkout action и линейный delivery progress. Dashboard chrome, rainbow statuses, карты с вымышленной геолокацией, gradients и декоративные метрики не используются.

Изображения продукта не подделываются. До появления реальных assets еда представляется типографикой, цветными material fields и короткими описаниями.

### 5. Motion только как state feedback

Допустимы короткие transitions выбора, quantity/status change и progress marker. Motion использует semantic tokens, не блокирует чтение и полностью отключается при `prefers-reduced-motion`.

## Risks / Trade-offs

- [Один экран может восприниматься как production IA] → явно назвать его journey preview и не добавлять URL/навигацию для вложенных стадий.
- [Fixture totals могут выглядеть как бизнес-правила] → хранить ставки/строки в feature fixture и документировать, что backend contract остаётся source of truth.
- [Высокая плотность ухудшит mobile] → обеспечить естественный DOM order, не использовать горизонтальные carousels и проверить 390px browser inspector.
- [Courier preview может смешаться с courier app] → показывать только customer-facing identity, ETA и progress; worker actions остаются вне scope.

## Migration Plan

1. Добавить feature fixtures, types и component tests.
2. Заменить placeholder element текущего route на journey preview, сохранив providers/layout.
3. Обновить localized messages и styles, затем проверить light/dark, keyboard, axe и три viewport.
4. Rollback: вернуть `PlaceholderPage`; foundation и API boundary не меняются.
