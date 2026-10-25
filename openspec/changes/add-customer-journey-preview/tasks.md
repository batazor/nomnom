# Tasks

## 1. Fixture model и presentation state

- [x] 1.1 Создать feature `customer-journey-preview` с typed restaurant, menu, cart, checkout, courier и delivery-stage fixtures; проверить unit-тестом стабильные identifiers, точные monetary values и согласованность выбранных items.
- [x] 1.2 Добавить локальный presentation reducer для выбора item, изменения quantity и переключения delivery stage; проверить reducer-тестами derived subtotal и отсутствие network/domain side effects.

## 2. Комплексный customer screen

- [x] 2.1 Реализовать на одном route restaurant/menu область с доступными selection и quantity controls; проверить keyboard interaction и локализованные labels component-тестом.
- [x] 2.2 Реализовать checkout summary и один primary CTA, связанные с fixture selection; проверить итог, адрес, payment summary и отсутствие business request component-тестом.
- [x] 2.3 Реализовать customer-facing active order и courier progress без worker actions, карты и внутренних FSM names; проверить смену presentation stage и доступную live-status semantics component-тестом.

## 3. Responsive composition и visual quality

- [x] 3.1 Заменить placeholder текущего route на комплексный preview, расширить typed i18n messages и semantic styles без новой runtime dependency; проверить `npm outdated --json`, typecheck и render shell test.
- [x] 3.2 Обеспечить desktop triptych и mobile DOM order без horizontal carousel/overflow, light/dark и `prefers-reduced-motion`; проверить axe, keyboard flow и tests на viewport 390px.
- [x] 3.3 Обновить `boundaries/ui/README.md`, явно обозначив fixture-preview и его non-production ограничения; проверить локальные Markdown links.

## 4. Интеграционная проверка

- [x] 4.1 Выполнить `npm run check` и `npm run build`, затем `design-taste-frontend` lint/inspect на 1440, 768 и 390px; устранить FAIL и недопустимые WARN, визуально проверить все screenshots.
- [x] 4.2 Выполнить `openspec validate add-customer-journey-preview --strict` и `git diff --check`; подтвердить отсутствие GraphQL business calls, payment/auth logic, courier worker actions и новых runtime dependencies.

## Workflow follow-up

- После review реализации архивировать change через OpenSpec archive workflow.
