# Restaurants

Контекст описывает предложение ресторана и выполнение принятого рестораном заказа. Его модель заказа относится только к работе ресторана.

## Language

**Restaurant**:
Участник системы, который предлагает меню и готовит принятые заказы.
_Avoid_: Vendor, kitchen

**Market**:
Город, в котором работает доставка; ресторан принадлежит одному неизменяемому Market, его меню и Restaurant Order — Market ресторана ([ADR 41](../../docs/adr/0041-cell-based-architecture-with-control-plane.md)).
_Avoid_: Region, tenant, city как идентификатор

**Menu Item**:
Доступное для заказа блюдо с названием и текущей ценой в меню ресторана.
_Avoid_: Product, order item

**Restaurant Order**:
Представление заказа, необходимое ресторану для принятия решения и приготовления; aggregate кухонного lifecycle.
_Avoid_: Order, customer order

**Kitchen Order**:
Роль Restaurant Order в процессе кухни после Acceptance; не отдельная сущность.
_Avoid_: KitchenOrder как имя aggregate

**Order Acceptance**:
Согласие открытого ресторана приготовить все доступные позиции полученного заказа.
_Avoid_: Order confirmation, payment authorization

**Preparation**:
Период между началом приготовления принятого заказа и его готовностью.
_Avoid_: Delivery, fulfillment

**Kitchen Queue**:
Бизнес-очередь принятых Restaurant Order, ожидающих начала Preparation.
_Avoid_: Kafka queue, task queue

**Kitchen Board**:
Read models кухни одного ресторана для её сотрудников: очередь, входящие, активные, готовые и запланированные заказы.
_Avoid_: Order flow board, Kitchen Queue

**Preparation Item**:
Позиция Restaurant Order в работе кухни.
_Avoid_: Menu Item, order item

**Assembly**:
Сборка приготовленных позиций в один заказ перед готовностью.
_Avoid_: Pickup

**Ready for Pickup**:
Состояние Restaurant Order, в котором заказ можно передать курьеру.
_Avoid_: Delivered, completed

Нормативные определения и остальные термины кухни — в [Ubiquitous Language](../../docs/ubiquitous-language.md#restaurants) и [Kitchen](../../docs/kitchen.md).
