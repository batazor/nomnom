# Delivery

Контекст описывает доступность курьеров, их назначение и физическое выполнение доставки.

## Language

**Courier**:
Исполнитель, который может быть доступен для одной доставки или занят ею.
_Avoid_: Driver, delivery agent

_Допущение_: курьер — пользователь платформы с тем же идентификатором (`courier_id` = `user_id`). Поэтому Register Courier пишет `courier:<id>#self@user:<id>`, а назначение — `delivery:<id>#courier@user:<courier_id>`.

**Market**:
Город, в котором работает доставка (ADR 41): курьер работает в одном Market, доставка принадлежит Market заказа. Курьеру предлагаются и назначаются только доставки его Market. `market_id` задаётся при регистрации курьера и создании доставки и не меняется.
_Avoid_: City (в коде), region, tenant, zone

**Courier Availability**:
Может ли курьер получать новые назначения: доступен, занят доставкой или вне линии.
_Avoid_: Location, shift

**Delivery**:
Перемещение заказа от ресторана к клиенту назначенным курьером.
_Avoid_: Shipment, order

**Courier Assignment**:
Закрепление доступного курьера за ожидающей доставкой.
_Avoid_: Offer, booking

**Delivery Offer**:
Ограниченное по времени предложение курьеру принять конкретную доставку; само по себе не закрепляет курьера за доставкой. Закрывается, когда курьер принимает или отклоняет его либо когда принят другой offer той же доставки.

**Assignment Workflow**:
Durable процесс поиска курьера для одной доставки — от создания до Pickup: предлагает доставку курьерам по очереди, держит срок каждого offer и возвращает доставку в поиск после снятия курьера. Состояние доставки не хранит: каждый шаг читает сохранённое.
_Avoid_: Dispatch, dispatcher (диспетчер — человек Courier Pool)

**Courier Candidate**:
Доступный курьер с известной позицией: только такого курьера поиск может выбрать. Курьер, ставший кандидатом, будит одну ожидающую доставку своего Market.
_Avoid_: Free courier

**Wake**:
Сигнал Assignment Workflow о том, что состояние доставки могло измениться; решения не несёт.
_Avoid_: Event, command
_Avoid_: Courier Assignment, booking

**Delivery Execution**:
Выполнение назначенной доставки: Pickup, передача заказа клиенту, отказ или отмена.
_Avoid_: Assignment, fulfillment

**Pickup**:
Получение назначенным курьером заказа у ресторана.
_Avoid_: Acceptance, collection request

**Delivery Completion**:
Окончательное состояние доставки после передачи заказа клиенту.
_Avoid_: Order completion, preparation completion

**Delivery Failure**:
Окончательное отрицательное состояние назначенной доставки с причиной, когда передать заказ клиенту невозможно.
_Avoid_: Order failure, technical error

**Delivery Cancellation**:
Окончательная отмена доставки по запросу процесса исполнения заказа до Pickup; назначенный курьер освобождается. После Pickup отмена отклоняется.
_Avoid_: Delivery Failure, Reassignment

**Delivery Duration Statistics**:
Read model `delivery` по Market: последние длительности Create Delivery → Pickup (`courier_lead`) и Pickup → вручение (`transit`), которые consumer собирает из собственных фактов Delivery. Отменённая или неудавшаяся доставка выбывает без длительности.
_Avoid_: ETA, delivery time (без уточнения вида)

**Delivery Duration Estimate**:
Ожидаемые `courier_lead` и `transit` доставки в Market: медианы Delivery Duration Statistics, пока выборка не набрана — базовые значения из конфигурации. По ней `orders` планирует запланированный заказ.
_Avoid_: Expected time (FR-610, время, названное курьером), promise
