# Design

## Decisions

### Source of truth и writers

| Relation | Source of truth | Запись |
| --- | --- | --- |
| `restaurant:<id>#owner@user:<owner_id>` | `Restaurant.ownerId` | `TOUCH` в Register Restaurant до сохранения |
| `restaurant:<id>#staff@user:<user_id>` | `Restaurant.staff` (новое множество в агрегате) | Assign Staff: `TOUCH` до сохранения; Revoke Staff: `DELETE` до сохранения |
| `courier:<id>#self@user:<id>` | зарегистрированный `Courier` | `TOUCH` в Register Courier до сохранения |
| `delivery:<id>#courier@user:<courier_id>` | `Delivery.courier_id` | `TOUCH` при Accept Delivery Offer (Assignment); `DELETE` при Reassign Courier и Cancel Delivery |

Все записи синхронные и выполняются до commit (dual-write rules в `docs/security.md`). Ошибка SpiceDB отменяет операцию (fail closed). Если запись прошла, а commit нет, лишнюю связь удалит reconciliation. `DELETE` до commit безопаснее для доступа: при неуспешном commit связь временно пропадает, и её восстановит reconciliation. Устаревшего доступа при этом не остаётся. Курьер — пользователь с тем же id (`courier_id` = `user_id`), это допущение фиксируется в glossary `delivery`.

Assign Staff / Revoke Staff: subject должен иметь `restaurant:<id>#manage`; staff — идемпотентное множество user id, повтор — no-op без записи в SpiceDB.

### Reconciliation

Правила как у `orders` (`slices/order/infrastructure/reconcile`): graph → business постранично по `ReadRelationships`, business → graph по хранилищу. Orphan или stale связь удаляется только после двух проходов с интервалом не меньше grace. Missing связь восстанавливается `TOUCH` сразу. Каждый проход пишет в log счётчики `checked`, `orphans_pending`, `orphans_deleted`, `missing_repaired`.

Хранилища `restaurants` и `delivery` пока в памяти процесса, поэтому reconciler — фоновый цикл внутри основного процесса (`RECONCILER__INTERVAL` 5m, `RECONCILER__GRACE` 10m, `RECONCILER__PAGE_SIZE` 500). Цикл останавливается до drain при shutdown. С постоянным хранилищем он станет отдельным workload.

### Проверка

Unit-тесты use cases с fake writer'ом. Integration-тесты против SpiceDB из `make spicedb-dev`: после регистрации владелец получает `manage`, staff — `operate_kitchen`, курьер — `act`, назначенный курьер — `act_as_courier`. После Revoke, Reassign и Cancel доступ пропадает. Reconciler удаляет сироту после grace и восстанавливает потерянную связь.
