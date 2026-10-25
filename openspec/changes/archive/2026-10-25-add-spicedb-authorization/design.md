# Design

## Context

Порты authorization уже объявлены рядом с use cases (см. таблицу в `docs/security.md`). ADR 27 даёт `service:<name>` subject. SpiceDB v1.57.0 проверен локально с `--datastore-bootstrap-files authorization/bootstrap.yaml` (рабочий каталог — `authorization/`, `schemaFile: schema.zed`).

## Decisions

### Platform capabilities для fulfillment

Worker создаёт и адресует ресурсы по `order_id` раньше, чем у них появляются relationships, поэтому его права — permissions синглтона `platform:nomnom`, выданные relation `fulfillment@service:orders-worker`. Grants принадлежат развёртыванию (`bootstrap.yaml`), а не коду.

### Соответствие портов и permissions (нормативно)

| Контекст | Порт / permission | SpiceDB check |
| --- | --- | --- |
| orders | `view` на `order:<id>` | `order:<id>#view` |
| orders | `cancel` на `order:<id>` | `order:<id>#cancel` |
| orders | `view_orders` на `customer:<id>` | `customer:<id>#view_orders` |
| orders | `Relationships.GrantOrderCustomer` | `TOUCH order:<id>#customer@user:<customer_id>` |
| payments | `create`, `authorize`, `capture`, `cancel_authorization`, `refund` | `platform:nomnom#manage_payments` |
| payments | `view`, `view_operations` | `platform:nomnom#view_payments` |
| restaurants | `canRegisterRestaurant` | `platform:nomnom#register_restaurant` |
| restaurants | `canManageRestaurant(id)` | `restaurant:<id>#manage` |
| restaurants | `canManageMenu(id)` | `restaurant:<id>#manage_menu` |
| restaurants | `canOperateKitchen(id)` | `restaurant:<id>#operate_kitchen` |
| restaurants | `canCoordinateFulfillment(id)` | `platform:nomnom#coordinate_fulfillment` |
| restaurants | `canViewRestaurantOrders(id)` | `restaurant:<id>#view_orders` |
| delivery | `RegisterCourier` на `Platform` | `platform:nomnom#register_courier` |
| delivery | `CreateDelivery`, `FindCourier`, `OfferDelivery`, `ReassignCourier`, `CancelDelivery`, `ReplanDelivery` (любой resource) | `platform:nomnom#coordinate_delivery` |
| delivery | прочие permissions на `Courier(id)` | `courier:<id>#act` |
| delivery | прочие permissions на `Delivery(id)` | `delivery:<id>#act_as_courier` |

Subject: `user` → `user:<id>`, `service` → `service:<id>`.

### Клиент и отказы

- Endpoint `SPICEDB__ENDPOINT`, bearer `SPICEDB__PRESHARED_KEY` (Secret, не в values), `SPICEDB__INSECURE=true` только для plaintext в kind/локально, deadline `SPICEDB__TIMEOUT` (2s).
- Consistency `minimize_latency`; `orders` после собственного `TOUCH` проверяет `order:<id>` с `at_least_as_fresh` по ZedToken, который адаптер держит в памяти процесса (персистентная authorization revision — вместе с PostgreSQL).
- Ошибка, timeout или недоступность SpiceDB — существующая ошибка «authorization unavailable» каждого контекста (fail closed, gRPC `UNAVAILABLE`); `PERMISSIONSHIP_NO_PERMISSION` и conditional — отказ.
- Пустой `SPICEDB__ENDPOINT` — ошибка startup процесса, проверяющего права (API контекстов); worker `orders` SpiceDB не использует. Runtime contract и локальный запуск передают endpoint явно; клиенты подключаются лениво.

### Проверка

Unit-тесты маппинга с fake клиентом; integration-тесты каждого контекста против SpiceDB из `make spicedb-dev` (`SPICEDB_ENDPOINT=localhost:50051`, ключ `devkey`): `orders-worker` получает fulfillment permissions, другой service — нет; `orders` после `GrantOrderCustomer` даёт `view`/`cancel` customer'у.

### kind

Deployment `spicedb` (memory datastore, bootstrap из ConfigMap со `schema.zed` и `bootstrap.yaml`, preshared key из Secret) в namespace `nomnom-authz`; NetworkPolicy egress к нему из контекстов; values `SPICEDB__*`.

## Risks / Trade-offs

- [Platform capability шире resource-level] → только для fulfillment service; пользовательские права остаются на ресурсах.
- [Memory datastore в kind] → grants восстанавливаются bootstrap'ом при рестарте; relationships заказов теряются — приемлемо для тестового окружения.
- [Нет writers для restaurant/courier/delivery relations] → соответствующие пользовательские checks отклоняются до отдельных changes.
