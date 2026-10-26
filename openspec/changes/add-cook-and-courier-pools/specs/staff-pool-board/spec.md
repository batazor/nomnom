# Spec Delta

## ADDED Requirements

### Requirement: Cook Pool в BFF
**ID:** FR-622

BFF SHALL отдавать `Kitchen.cookPool` (`size`, `onLine`, `busy`, `queued`, список Cook с `id`, `name`, `onLine` и текущим заказом) и поле `cook` с именем у заказов кухни в `PREPARING`, `ASSEMBLED` и `READY`. `kitchenChanged(restaurantId)` SHALL приходить и после изменения Cook ресторана.

#### Scenario: Повар встаёт на линию
**ID:** AC-793

- **WHEN** сотрудник ставит Cook ресторана на линию
- **THEN** подписчики `kitchenChanged` ресторана получают kitchen с увеличенным `cookPool.onLine`

### Requirement: Команды Cook Pool в BFF
**ID:** FR-625

BFF SHALL предоставлять mutations `hireCook(restaurantId, name)`, `setCookOnLine(cookId, onLine)` и `retireCook(cookId)`, вызывающие RPC `restaurants` от имени пользователя и возвращающие обновлённый `cookPool`. Бизнес-отказы `CookBusy`, `CookOnLine` и `NoCookAvailable` MUST возвращаться как problem code, а не как ошибка.

#### Scenario: Снятие занятого повара
**ID:** AC-799

- **WHEN** сотрудник вызывает `setCookOnLine(cookId, false)` для Cook с заказом в `PREPARING`
- **THEN** ответ содержит problem code `COOK_BUSY` и неизменный `cookPool`

### Requirement: Courier Pool в BFF
**ID:** FR-623

BFF SHALL отдавать query `courierPool` Market запроса (заголовок `X-Nomnom-Market`; `id` пула — `market_id`, `size`, `onLine`, `busy`, `waiting`, список курьеров), subscription `courierPoolChanged(marketId)`, отдающую только пул этого Market, и mutations `addCourier(name)`, `setCourierOnLine(courierId, onLine)`, `retireCourier(courierId)`. `addCourier` SHALL регистрировать курьера в Market запроса с новым UUIDv7 как `courier_id`.

#### Scenario: Диспетчер добавляет курьера
**ID:** AC-794

- **WHEN** диспетчер Market `berlin` вызывает `addCourier("Анна")`
- **THEN** в `courierPool` Market `berlin` есть available курьер «Анна», подписчики `courierPoolChanged(marketId: "berlin")` получают пул с ней, а подписчики другого Market — нет

### Requirement: Имена исполнителей в BFF
**ID:** FR-626

BFF SHALL разрешать имена курьеров и поваров по id через query контекста-владельца; в Integration Events имена MUST NOT передаваться. `Order` покупателя SHALL содержать имя назначенного курьера и MUST NOT содержать имя повара.

#### Scenario: Покупатель видит курьера
**ID:** AC-795

- **GIVEN** заказу покупателя назначен курьер «Тимур»
- **WHEN** покупатель запрашивает `order(id)`
- **THEN** ответ содержит имя курьера «Тимур» и не содержит имени повара

### Requirement: Пулы на flow-board
**ID:** FR-624

Шапки колонок кухни и курьеров flow-board SHALL показывать пул: «линия N из M», занятых из линии с полосой загрузки, backlog (очередь кухни, доставки без курьера) и список работников со статусом, текущей работой, действиями «на линию», «снять», «убрать» и формой добавления по имени. Пустая линия кухни при непустой очереди MUST показываться предупреждением.

#### Scenario: Загрузка в шапке
**ID:** AC-796

- **GIVEN** на линии 3 повара из 5, двое готовят, в очереди 4 заказа
- **WHEN** сотрудник открывает flow-board
- **THEN** шапка кухни показывает «Линия 3 из 5», «занято 2/3» и «В очереди: 4»

#### Scenario: Нет поваров на линии
**ID:** AC-797

- **GIVEN** у ресторана нет поваров на линии, а в очереди есть заказ
- **WHEN** сотрудник открывает flow-board
- **THEN** колонка кухни показывает предупреждение, что на линии нет поваров

### Requirement: Исполнители на карточках и видимость пула курьеров
**ID:** FR-627

Карточки заказов flow-board SHALL показывать имя повара и курьера. Пул курьеров MUST показываться только тем, кому `courierPool` не отвечает `PermissionDenied`. Пулы обновляются только subscriptions, без polling.

#### Scenario: Пользователь без права на пул курьеров
**ID:** AC-798

- **WHEN** `courierPool` отвечает `PermissionDenied`
- **THEN** колонка курьеров показывает только собственную работу пользователя без пула

#### Scenario: Имя повара на карточке
**ID:** AC-800

- **GIVEN** заказ готовит Cook «Айгуль»
- **WHEN** сотрудник смотрит flow-board
- **THEN** карточка заказа показывает «Готовит: Айгуль»
