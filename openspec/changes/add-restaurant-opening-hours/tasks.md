# Tasks

## 1. restaurants

- [x] 1.1 `timeZone` в Register Restaurant, валидация IANA, migration документов; `restaurants.proto` (`FR-201`, `AC-202`, `AC-938`).
- [x] 1.2 `openingHours`, Set Opening Hours и gRPC `SetOpeningHours`, проверка пересечений (`FR-696`, `FR-697`, `AC-939`–`AC-942`).
- [x] 1.3 Перевод интервалов в моменты с переводом часов; unit-тесты на обе ночи перевода (`FR-698`, `AC-943`, `AC-944`).
- [x] 1.4 Приём заказов по флагу и расписанию: адаптер acceptance conditions, Get Restaurant с `accepting_orders` и `next_opening_at`, List Restaurants, кеш каталога (`FR-699`, `FR-204`, `FR-205`, `AC-945`–`AC-948`, `AC-211`, `AC-213`).

## 2. BFF и UI

- [x] 2.1 BFF: `Restaurant.timeZone`, `openingHours`, `acceptingOrders`, `nextOpeningAt`, mutation `setOpeningHours`; тесты (`FR-700`, `AC-949`, `AC-950`).
- [x] 2.2 UI: редактор расписания на экране управления, «Откроется в …» в каталоге; i18n, моки, generated types, тесты (`FR-701`, `FR-702`, `AC-951`, `AC-952`).

## 3. ops и документация

- [x] 3.1 Seed расписания демо-ресторанов, kind e2e.
- [x] 3.2 `docs/kitchen.md`, `docs/specification-pattern.md`, `docs/ubiquitous-language.md`, README `restaurants`, ADR 0002 `restaurants`.

## 4. Проверка

- [ ] 4.1 Проверки сервисов, `make buf-breaking`, `openspec validate add-restaurant-opening-hours --strict`, kind e2e.
