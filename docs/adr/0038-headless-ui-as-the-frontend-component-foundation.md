# 38. Headless UI as the Frontend Component Foundation

Date: 2026-10-25

## Status

Accepted

Supersedes [ADR 23](0023-astra-ui-as-the-frontend-component-foundation.md) в части component layer; остальные решения ADR 23 (feature boundaries, semantic tokens, GraphQL как единственная API boundary браузера) остаются в силе.

## Context

ADR 23 выбрал Astra (`@astra-rn/react`, `@astra-rn/styles`) styled-слоем поверх React Aria Components. На практике nomnom перекрашивал почти каждый Astra-компонент своими tokens, а внешний вид и DOM-структура Astra диктовали CSS-переопределения (`.switch__control` и т. п.).

## Decision

Интерактивные UI-компоненты строятся на [Headless UI](https://headlessui.com/) (`@headlessui/react`, exact pin). Headless UI даёт поведение и accessibility (keyboard, focus, ARIA, focus trap, возврат фокуса), а весь внешний вид задают собственные semantic tokens и CSS в `src/shared/ui/` и `src/styles.css`.

Astra и React Aria Components удаляются из зависимостей. Headless UI импортируется только внутри `src/shared/ui/` (проверяется `scripts/check-boundaries.mjs`); feature и `app/` используют локальную façade. Новый интерактивный примитив (Dialog, Menu, Listbox, Combobox, Tabs, Disclosure, Popover, Radio Group, Checkbox и т. д.) сначала ищется в Headless UI и оборачивается в `shared/ui/`; `div`-эмуляция и второй component framework не добавляются.

## Consequences

- Стилизация полностью своя; Headless UI отдаёт состояние через `data-*` атрибуты (`data-checked`, `data-disabled`, `data-hover`, `data-active`, `data-invalid`, `data-closed`), CSS опирается на них.
- Façade-API следует Headless UI: `onClick`, `disabled`, `checked`/`onChange`, `required`. Собственные расширения (`variant`, `size`, `fullWidth`, `isPending`) живут только в façade.
- Accessibility продолжает проверяться component-тестами и axe.
