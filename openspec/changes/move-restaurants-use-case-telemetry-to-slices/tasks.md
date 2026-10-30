# Tasks

## 1. Перенос списков

- [ ] 1.1 Экспорт списков из `di/registrations.ts` шести слайсов с проверкой ключей по cradle (`FR-730`, `AC-995`).
- [ ] 1.2 `di/usecases.ts` без списков; сборка в `di/restaurants.ts` и `di/consumer.ts` (`FR-730`).
- [ ] 1.3 Тест состава инструментированных use cases обоих процессов (`AC-996`).

## 2. Документация

- [ ] 2.1 Комментарии в `slices/simulator/di/registrations.ts`, `di/restaurants.ts`, `di/consumer.ts`, `test/composition.test.ts`, `README.md`; `docs/observability.md`, если он называет расположение списка.

## 3. Проверка

- [ ] 3.1 `npm run check` и тесты `restaurants`; `openspec validate move-restaurants-use-case-telemetry-to-slices --strict`.
