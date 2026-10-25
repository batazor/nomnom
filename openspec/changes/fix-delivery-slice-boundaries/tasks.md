# Tasks

## 1. Get Courier Current Delivery

- [x] 1.1 Перенести `courier/applications/get_courier_current_delivery` вместе с read model и тестами в `delivery/applications`; обновить DI, memory/PostgreSQL адаптеры read model и gRPC `GetCourierCurrentDelivery`; поведение и тесты AC-310, AC-311 без изменений (`FR-304`).
- [x] 1.2 Убедиться, что `slices/courier` не импортирует `slices::delivery`; добавить тест или проверку в `make` (например, `grep` в test), чтобы цикл не вернулся (`FR-486`, `AC-551`).

## 2. Порт занятости курьера

- [x] 2.1 Объявить в `courier/applications` порт занятости (`ensure_available`, `occupy`, `release`) и реализацию поверх `CourierRepository` в текущей Unit of Work; ошибки с прежними причинами.
- [x] 2.2 Перевести use cases `delivery` (Find Courier, Offer, Accept Offer, Reassign, Replan, Confirm Delivery, Report Failure, Cancel) на порт; убрать прямые вызовы методов `Courier` и `CourierRepository` из `delivery/applications`; тесты атомарности (AC-328) и новые AC-549, AC-550 на memory store, PostgreSQL-тесты если доступна БД (`FR-485`).

## 3. Документация и проверка

- [x] 3.1 Записать исключение «offer, `Delivery` и `Courier` в одной транзакции» в `docs/unit-of-work.md` и README `delivery`; обновить описание структуры слайсов в README.
- [x] 3.2 `cargo test`, `cargo clippy --all-targets`, `rustfmt --check` на изменённых файлах; `openspec validate fix-delivery-slice-boundaries --strict`.
