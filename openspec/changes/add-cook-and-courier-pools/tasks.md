# Tasks

## 1. Общее

- [x] 1.1 `authorization/schema.zed` и `validation.yaml`: `restaurant#manage_cooks`, `platform#dispatcher`, `platform#manage_couriers` (`FR-614`, `FR-620`); `register_courier` удаляется в 3.2, когда `delivery` перейдёт на `manage_couriers`; `ops/kind/authz/bootstrap-kind.yaml` — demo-пользователь как `dispatcher`.
- [x] 1.2 GRPCRoute новых RPC в `ops/kind/manifests/gateway/gateway.yaml` (`restaurants-v1-cook-pool`, `GetCookPool` в `restaurants-v1-kitchen-reads`); `delivery` — в 3.2.

## 2. restaurants

- [x] 2.1 Слайс `cook`: aggregate, Hire/Put On Line/Take Off Line/Retire, Get Cook Pool, коллекция `cooks`, RPC (`FR-614`–`FR-616`, `AC-775`–`AC-781`).
- [x] 2.2 `restaurantorder`: назначение Cook в Start Preparation, `NoCookAvailable`, `cookId`, validator и partial index (`FR-225`, `FR-617`, `AC-782`–`AC-785`).
- [x] 2.3 `simulator`: старт только при свободном Cook, перепланирование от старта (`FR-618`, `AC-786`).

## 3. delivery

- [x] 3.1 `Courier.name`, Register Courier с именем, миграция `couriers` (`FR-301`, `AC-302`, `AC-787`).
- [x] 3.2 Retire Courier, Get Courier Pool, `manage_couriers` для Set Courier Availability, Change Notification пула, удаление `register_courier`, seed курьеров с именами и позициями, имя курьера e2e (`FR-619`–`FR-621`, `AC-788`–`AC-792`).

## 4. BFF и UI

- [ ] 4.1 BFF: перегенерировать клиентов, `cookPool`, `courierPool`, `courierPoolChanged`, mutations, имена; read имени курьера для покупателя заказа в `delivery` (`FR-622`, `FR-623`, `FR-625`, `FR-626`, `AC-793`–`AC-795`, `AC-799`).
- [ ] 4.2 UI: пулы в колонках flow-board, имена на карточках и в отслеживании, моки, тесты (`FR-624`, `FR-627`, `AC-796`–`AC-798`, `AC-800`).

## 5. Demo и проверка

- [x] 5.1 `seed.json` и `seed.py`: повара каждого ресторана (2.2), курьеры с именами и позициями (3.2).
- [ ] 5.2 Проверки каждого сервиса, `openspec validate add-cook-and-courier-pools --strict`, прогон в kind.
