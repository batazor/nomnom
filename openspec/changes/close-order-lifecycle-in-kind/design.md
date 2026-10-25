# Design

## Context

- Order Fulfillment Workflow (`orders-worker`, Temporal) последовательно авторизует оплату, запрашивает ресторан, после принятия создаёт доставку, после Pickup списывает оплату и завершает заказ после Delivery Completion. Факты кухни и доставки `orders-consumer` получает из Kafka.
- `delivery`: application-слой содержит все сценарии курьера; `courier#act` проверяет «subject — этот курьер», принадлежность доставки и предложения проверяет домен. Истечение предложений уже работает фоновым worker'ом процесса.
- Identity: projected ServiceAccount token (ADR 27) с allowlist вызывающих; user subject — только из BFF-signed assertion от делегата (ADR 32).

## Decisions

### 1. Dispatch — процесс назначения внутри `delivery`

Назначение курьера — ответственность `delivery`, а не оркестратора заказа. Use case **Dispatch Deliveries** обрабатывает пачку:

1. ожидающие доставки (`Waiting`) без pending-предложения, по возрастанию времени создания, не больше `batch_size`;
2. для каждой — в своём Unit of Work: кандидат через `select_candidate`, исключая курьеров, которым эту доставку уже предлагали; `make_offer`;
3. доставка без кандидата остаётся ожидающей и берётся в следующем цикле.

Без subject и без permission check, как Expire Delivery Offers: это работа процесса, а не вызов. Конкурентное изменение доставки (optimistic concurrency) пропускает её. Worker — тот же цикл, что у истечения предложений (`OFFERS__DISPATCH_INTERVAL`, по умолчанию 2 s), в процессе `delivery`.

Replan из `orders` по таймауту назначения остаётся эскалацией: он снимает назначение и ищет заново.

### 2. gRPC API курьера

Методы `nomnom.delivery.v1.DeliveryService`, по одному на сценарий, с теми же authorization и транзакциями, что у handlers:

| RPC | Subject | Проверка |
| --- | --- | --- |
| `RegisterCourier` | operator | `platform#register_courier` |
| `UpdateCourierLocation`, `SetCourierAvailability` | курьер | `courier#act` |
| `ListAvailableDeliveryOffers`, `AcceptDeliveryOffer`, `RejectDeliveryOffer` | курьер | `courier#act` |
| `ConfirmPickup`, `ConfirmDelivery` | курьер | `courier#act` |
| `ReportDeliveryFailure` | курьер или диспетчер | `courier#act` / `platform#dispatch_delivery` |

`courier_id` курьерских RPC — поле запроса; домен отклоняет действие над чужой доставкой или предложением. Ошибки переводятся существующей таблицей transport.

### 3. Входящие заказы и Get Kitchen Queue по gRPC

Get Kitchen Queue по своей спецификации содержит только принятые (`QUEUED`) заказы. Новый заказ ждёт решения кухни, поэтому нужен отдельный query **List Incoming Orders**: заказы в `RECEIVED` по возрастанию времени получения. Это доска «входящие» кухни, а не изменение смысла очереди.


`GetKitchenQueue(restaurant_id)` возвращает заказы очереди кухни в рекомендованном порядке с `restaurant_order_id`, `order_id`, состоянием и версией. Проверка — `restaurant#operate_kitchen`, как у команд кухни.

### 4. Kind: fulfillment-процессы `orders`

Chart `boundaries/orders/ops/helm` уже содержит `values-worker.yaml` и `values-forwarder.yaml`. В kind добавляются Skaffold-релизы `orders-worker` и `orders-forwarder` с values в `ops/kind/values/`. ServiceAccount `nomnom-orders/orders-worker` уже в allowlist `restaurants`, `delivery`, `payments`.

### 5. Кто играет кухню и курьера в e2e

У кухни и курьера пока нет клиентов и операций в BFF. Их роль в kind играет fixture **`e2e-driver`** (Deployment в `nomnom-bff` из образа `e2e/probe` с projected tokens для `nomnom.restaurants` и `nomnom.delivery`).

- Только kind-values `restaurants` и `delivery` добавляют `nomnom-bff/e2e-driver=e2e-driver` в allowlist и `e2e-driver` в `userDelegates`. Chart-default и другие окружения не меняются.
- User assertion для сотрудника, курьера и оператора выпускает тест тем же kind-ключом BFF, что и проверки раздела 7 (`scripts/mint-jwt.py`).
- Покупатель идёт настоящим путём: access token IdP-fixture → BFF GraphQL `placeOrder` и `order(id)`.

Заменять e2e-driver на операции BFF — дело будущих клиентов кухни и курьера.

### 6. Оператор платформы в kind

`platform:nomnom#operator` в общий `authorization/bootstrap.yaml` не входит: это grant конкретного человека. `scripts/authz.sh` в kind добавляет к bootstrap файл `ops/kind/authz/bootstrap-kind.yaml` со связью `platform:nomnom#operator@user:kind-operator`.

### 7. Тест `order-lifecycle`

Chainsaw-тест в `ops/kind/e2e/order-lifecycle/` с уникальными id на прогон:

1. Оператор: RegisterRestaurant (owner — тестовый владелец), RegisterCourier.
2. Владелец: AssignStaff, CreateMenu, AddMenuItem, OpenRestaurant.
3. Курьер: UpdateCourierLocation, SetCourierAvailability(available).
4. Покупатель: `placeOrder` через BFF.
5. Кухня: ожидает заказ в ListIncomingOrders → AcceptRestaurantOrder; заказ появляется в GetKitchenQueue.
6. Курьер: ожидает предложение в ListAvailableDeliveryOffers → AcceptDeliveryOffer.
7. Кухня: StartPreparation → MarkRestaurantOrderReady.
8. Курьер: ConfirmPickup → ConfirmDelivery.
9. Покупатель: `order(id)` в BFF имеет `status = DELIVERED`; milestones содержат `COURIER_ASSIGNED`, `PICKED_UP`, `DELIVERED`.

Ожидания — assert с таймаутом, а не sleep. Тест запускается `make kind-test` вместе с остальными.

## Risks / Trade-offs

- [Делегат `e2e-driver` расширяет доверие в kind] → только kind-values; его assertion подписаны тем же kind-ключом, что и у BFF; production-values не меняются.
- [Dispatch без расстояния предлагает любого свободного курьера с позицией] → это текущее поведение locator; выбор по расстоянию — отдельное решение.
- [Длинный e2e в общем кластере] → уникальные id на прогон, ничего не удаляется из чужих данных.
