# Tasks

## 1. Перенос RPC

- [ ] 1.1 `cook`, `simulator`, `scheduleslot`: перенос RPC и собственный тип зависимостей (`FR-731`, `AC-997`).
- [ ] 1.2 `menu`, `restaurant`: перенос RPC и чтения из `service.ts` (`FR-731`, `AC-997`).
- [ ] 1.3 `restaurantorder`: `kitchen.ts`, `kitchen-board.ts`, команды из `service.ts` (`FR-731`, `AC-997`).
- [ ] 1.4 Разбор `messages.ts` по слайсам, общий mapping в корне без импорта слайсов (`FR-731`).
- [ ] 1.5 `service.ts` только объединяет RPC и зависимости слайсов; импорты в `test/` (`FR-731`, `AC-997`, `AC-998`).

## 2. Документация

- [ ] 2.1 `boundaries/restaurants/README.md`: новые пути адаптеров; ADR 6: корень собирает proto-сервис, методы принадлежат слайсам.

## 3. Проверка

- [ ] 3.1 `npm run check` и тесты `restaurants`, включая gRPC integration; `openspec validate move-restaurants-grpc-to-slices --strict`.
