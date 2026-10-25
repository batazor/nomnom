# Delivery

Контекст описывает доступность курьеров, их назначение и физическое выполнение доставки.

## Language

**Courier**:
Исполнитель, который может быть доступен для одной доставки или занят ею.
_Avoid_: Driver, delivery agent

_Допущение_: курьер — пользователь платформы с тем же идентификатором (`courier_id` = `user_id`). Поэтому Register Courier пишет `courier:<id>#self@user:<id>`, а назначение — `delivery:<id>#courier@user:<courier_id>`.

**Courier Availability**:
Может ли курьер получать новые назначения: доступен, занят доставкой или вне линии.
_Avoid_: Location, shift

**Delivery**:
Перемещение заказа от адреса ресторана к адресу клиента назначенным курьером.
_Avoid_: Shipment, order

**Courier Assignment**:
Закрепление доступного курьера за ожидающей доставкой.
_Avoid_: Offer, booking

**Delivery Offer**:
Ограниченное по времени предложение курьеру принять конкретную доставку; само по себе не закрепляет курьера за доставкой. Закрывается, когда курьер принимает или отклоняет его либо когда принят другой offer той же доставки.
_Avoid_: Courier Assignment, booking

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
