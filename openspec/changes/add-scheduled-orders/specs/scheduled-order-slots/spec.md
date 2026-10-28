# Spec Delta

## ADDED Requirements

### Requirement: Ресторан задаёт ёмкость окон доставки
**ID:** FR-672

`SetScheduledSlotCapacity` SHALL задавать `scheduledSlotCapacity` ресторана — целое больше 0 — или убирать её (`clear`) и MUST требовать `restaurant#manage`. Ресторан без ёмкости MUST NOT принимать запланированные заказы.

#### Scenario: Владелец задаёт ёмкость
**ID:** AC-894

- **WHEN** владелец ресторана задаёт ёмкость 4
- **THEN** ресторан принимает до 4 запланированных заказов на окно

#### Scenario: Неположительная ёмкость
**ID:** AC-895

- **WHEN** владелец задаёт ёмкость 0
- **THEN** вызов отклоняется `INVALID_ARGUMENT`, ёмкость не меняется

### Requirement: Конфиг окон обязателен
**ID:** FR-694

`SCHEDULED_SLOT__WIDTH`, `SCHEDULED_SLOT__MIN_LEAD`, `SCHEDULED_SLOT__MAX_HORIZON`, `SCHEDULED_SLOT__HOLD_TTL` и `SCHEDULED_SLOT__ALTERNATIVES` SHALL задаваться явно. Пустое или невалидное значение, ширина, не делящая 30 минут, или `MIN_LEAD ≥ MAX_HORIZON` MUST останавливать процесс `restaurants` на старте.

#### Scenario: Ширина не делит 30 минут
**ID:** AC-937

- **WHEN** процесс `restaurants` стартует с `SCHEDULED_SLOT__WIDTH=20m`
- **THEN** загрузка конфигурации завершается ошибкой

### Requirement: List Scheduled Slots возвращает окна с доступностью
**ID:** FR-673

List Scheduled Slots SHALL возвращать окна ресторана шириной `SCHEDULED_SLOT__WIDTH`, выровненные по UTC кратно ширине, от первой границы не раньше `now + MIN_LEAD` до `now + MAX_HORIZON`, каждое с `start`, `end` и `available`. Окно MUST быть недоступно, если активных резерваций не меньше ёмкости; активная резервация — `confirmed` или `held` с непросроченным hold. Ресторан без ёмкости или закрытый SHALL получать пустой список. Query SHALL быть публичным, как Get Restaurant.

#### Scenario: Окно заполнено
**ID:** AC-896

- **GIVEN** ёмкость 2, в окне 19:00–19:15 одна `confirmed` и одна непросроченная `held` резервация
- **WHEN** покупатель запрашивает окна ресторана
- **THEN** окно 19:00–19:15 возвращается с `available = false`

#### Scenario: Просроченный hold освобождает место
**ID:** AC-897

- **GIVEN** ёмкость 1, в окне только `held` резервация с истёкшим hold
- **WHEN** покупатель запрашивает окна ресторана
- **THEN** окно возвращается с `available = true`

### Requirement: Reserve Scheduled Slot держит место окна
**ID:** FR-674

Reserve Scheduled Slot SHALL принимать `restaurant_id`, `window_start` и `order_id` от `service` identity `orders` и создавать резервацию `held` с `expiresAt = now + HOLD_TTL`, возвращая окно. Окно не на сетке, раньше `now + MIN_LEAD` или позже горизонта MUST давать `INVALID_ARGUMENT`; закрытый ресторан или ресторан без ёмкости — `FAILED_PRECONDITION` `SCHEDULED_ORDERS_UNAVAILABLE`. Повтор с тем же `order_id` MUST возвращать ту же резервацию.

#### Scenario: Место есть
**ID:** AC-898

- **WHEN** `orders` резервирует свободное окно 19:00 для заказа
- **THEN** создана резервация `held`, ответ — окно 19:00–19:15

### Requirement: Ёмкость окна не превышается
**ID:** FR-695

Резервация заполненного окна SHALL отклоняться `FAILED_PRECONDITION` `SLOT_UNAVAILABLE` с не более чем `SCHEDULED_SLOT__ALTERNATIVES` ближайшими доступными окнами. Конкурентные резервации MUST NOT превышать ёмкость окна.

#### Scenario: Окно заполнено
**ID:** AC-899

- **GIVEN** окно 19:00 заполнено, окна 19:15 и 19:45 свободны, `ALTERNATIVES = 2`
- **WHEN** `orders` резервирует окно 19:00
- **THEN** ответ `SLOT_UNAVAILABLE` с окнами 19:15 и 19:45, резервация не создана

#### Scenario: Последнее место
**ID:** AC-900

- **GIVEN** в окне осталось одно место
- **WHEN** два заказа одновременно резервируют это окно
- **THEN** одна резервация создана, второй заказ получает `SLOT_UNAVAILABLE`

### Requirement: Резервация подтверждается и освобождается
**ID:** FR-675

Confirm Scheduled Slot SHALL переводить резервацию заказа из `held` в `confirmed`; повтор MUST быть no-op. Если hold истёк и резервация вычищена, подтверждение SHALL заново проверять ёмкость и при заполненном окне отвечать `SLOT_UNAVAILABLE`. Release Scheduled Slot SHALL удалять резервацию заказа; отсутствующая резервация MUST давать успешный no-op. Уменьшение ёмкости MUST NOT затрагивать существующие резервации.

#### Scenario: Подтверждение после оплаты
**ID:** AC-901

- **WHEN** `orders` подтверждает `held` резервацию заказа
- **THEN** резервация `confirmed` и больше не истекает

#### Scenario: Hold истёк, окно заполнили
**ID:** AC-902

- **GIVEN** hold заказа истёк, а окно заняли другие заказы
- **WHEN** `orders` подтверждает резервацию
- **THEN** ответ `SLOT_UNAVAILABLE`, резервация не создана

#### Scenario: Повтор освобождения
**ID:** AC-903

- **WHEN** `orders` повторно освобождает уже освобождённую резервацию
- **THEN** вызов успешен, окно не меняется
