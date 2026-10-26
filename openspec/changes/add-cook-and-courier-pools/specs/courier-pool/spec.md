# Spec Delta

## ADDED Requirements

### Requirement: Retire Courier
**ID:** FR-619

`delivery` SHALL предоставлять Retire Courier и RPC `RetireCourier(courier_id)`: выводит `Courier` в состоянии unavailable из Courier Pool. Для available или busy `Courier` команда MUST отклоняться как `CourierOnLine`; повтор для выбывшего — no-op. Выбывший `Courier` MUST NOT получать Delivery Offer и Assignment и MUST NOT менять доступность.

#### Scenario: Курьер вне линии выбывает
**ID:** AC-788

- **WHEN** диспетчер вызывает `RetireCourier` для unavailable курьера
- **THEN** курьер не входит в Courier Pool и не получает новых офферов

#### Scenario: Курьера на линии нельзя убрать
**ID:** AC-789

- **WHEN** диспетчер вызывает `RetireCourier` для available курьера
- **THEN** ответ `CourierOnLine`, курьер остаётся в пуле и на линии

### Requirement: Диспетчер управляет Courier Pool
**ID:** FR-620

Register Courier, Retire Courier и Get Courier Pool MUST требовать `platform#manage_couriers` (relation `operator` или `dispatcher`). Set Courier Availability SHALL разрешаться subject с `courier#act` на этого курьера или с `platform#manage_couriers`; правила доступности FR-302 не меняются.

#### Scenario: Диспетчер ставит курьера на линию
**ID:** AC-790

- **WHEN** диспетчер устанавливает доступность available чужому unavailable курьеру
- **THEN** курьер available и может получить оффер

#### Scenario: Пользователь без права
**ID:** AC-791

- **WHEN** пользователь без `dispatcher` и `operator` вызывает `GetCourierPool` или меняет доступность чужого курьера
- **THEN** ответ `PERMISSION_DENIED`, состояние не меняется

### Requirement: Get Courier Pool
**ID:** FR-621

`delivery` SHALL предоставлять query Get Courier Pool и RPC `GetCourierPool()`: `size` — невыбывшие курьеры, `on_line` — available и busy, `busy`, `waiting` — Delivery в `Waiting`, и список курьеров с именем, доступностью, pending offer и активной Delivery. Query MUST NOT менять состояние. Изменение курьера, оффера или Delivery SHALL публиковать Change Notification Courier Pool.

#### Scenario: Загрузка курьеров
**ID:** AC-792

- **GIVEN** в пуле 6 курьеров, 4 на линии, 3 busy, 2 Delivery ждут курьера
- **WHEN** диспетчер вызывает `GetCourierPool`
- **THEN** ответ `size = 6`, `on_line = 4`, `busy = 3`, `waiting = 2` и шесть курьеров с именами
