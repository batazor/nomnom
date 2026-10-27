# 36. GraphQL subscriptions через Cosmo Streams и Order Change Notifications

Date: 2026-10-25

## Status

Accepted

Заменяет решение «bounded polling» для экрана заказа ([docs/ui.md](../ui.md)). Уточняет границу
BFF из [ADR 2 BFF](../../boundaries/bff/docs/adr/0002-bff-as-the-client-entry-point.md): Router
читает один Kafka-топик уведомлений.

## Context

Экран заказа обновлялся опросом `order(id)` раз в 10 с, flow board (заказы, кухня, курьер) — раз в 3 с. Каждый
открытый экран нагружал BFF и `orders` без изменений. Задержка равнялась интервалу опроса.
Изменения заказа и так проходят через Kafka (ADR 13, ADR 25), но до клиента не доходили.

Ограничения:

- BFF — Cosmo Router с Go router plugin (локальный ADR 3). gRPC-сервисы и plugins Router
  [не поддерживают streaming и subscriptions](https://cosmo-docs.wundergraph.com/router/gRPC/concepts),
  поэтому подписку не реализовать резолвером plugin.
- Integration Events закодированы Protobuf и являются контрактом между bounded contexts. Router
  Cosmo Streams принимает только JSON с `__typename`. Протащить Integration Events к клиенту —
  значит раскрыть внутренние контракты и данные.
- Права на заказ проверяет `orders` (SpiceDB, ADR 18), а не BFF или Router.

Рассмотренные варианты:

1. **Subscription-резолвер в отдельном GraphQL subgraph** (сервер с `graphql-ws`/SSE,
   читающий Kafka). Это новый stateful-сервис со своим runtime, авторизацией и масштабированием.
2. **Cosmo Streams на топиках Integration Events + Custom Module Router** (`OnReceiveEvents`
   декодирует Protobuf и фильтрует события по subject). Нужна собственная сборка Router вместо
   закреплённого релиза (ADR 10). Router начинает зависеть от каждого Protobuf-контракта, а
   авторизация дублирует логику `orders`.
3. **Cosmo Streams на отдельном топике уведомлений, данные — через entity resolution plugin.**

## Decision

Вариант 3: событие несёт только ключ сущности, состояние Router получает обычным запросом BFF с
токеном подписчика (паттерн notification + refetch).

- **Order Change Notification.** `orders` при каждой записи Order (`Add`, `Save` в
  `ProcessStore`) кладёт в тот же Transactional Outbox сообщение в топик
  `orders.v1.order-changes`: JSON `{"__typename":"Order","id":"<order_id>"}`, ключ партиции —
  `order_id`. Это не Integration Event: его не потребляет ни один bounded context, в нём нет
  данных заказа, envelope ADR 25 не нужен. Через `ProcessStore` проходят все изменения заказа:
  размещение, Fulfillment Facts, отмена, завершение. Поэтому уведомление не теряется и не
  обгоняет commit (ADR 13).
- **Kitchen и Courier — по тому же образцу.** `restaurants` при каждой записи Restaurant Order
  и Restaurant кладёт `{"__typename":"Kitchen","restaurantId":…}` в
  `restaurants.v1.kitchen-changes`. Notifier сам открывает Unit of Work вокруг записи или
  присоединяется к активному: часть use cases сохраняет агрегат без UoW. `delivery` кладёт
  `{"__typename":"Courier","id":…}` в `delivery.v1.courier-changes` при каждой записи курьера,
  оффера и доставки, затронувшей курьера, включая прежнего курьера переназначенной доставки.
  Запись курьера и уведомление — один SQL-оператор (CTE): регистрация и смена доступности идут
  без UoW. `Kitchen @key(fields: "restaurantId")` и `Courier @key(fields: "id")` резолвят
  `LookupKitchenByRestaurantId` (права кухни проверяет `restaurants`) и `LookupCourierById`
  (ключ должен совпадать с subject токена, иначе `NOT_FOUND`). `Courier.id` — subject
  пользователя, его отдаёт query `courier`.
- **Event-driven subgraph.** `boundaries/bff/events/schema.graphql` объявляет
  `orderChanged(id: ID!): Order!` с `@edfs__kafkaSubscribe(topics: ["orders.v1.order-changes"])`
  и `@openfed__subscriptionFilter` по `id`. Сервера у subgraph нет: топик читает сам Router
  (`events.providers.kafka`, `BFF__KAFKA_BROKERS`).
- **Данные и права.** `Order` в схеме plugin — сущность `@key(fields: "id")`. На каждое событие
  Router вызывает `LookupOrderById` plugin с `Authorization` подписчика (правило `propagate`).
  Plugin выполняет тот же сценарий, что `order(id)`: `orders` от имени покупателя, ADR 32. Чужой
  или отсутствующий заказ — `NOT_FOUND`, подписчик получает error-событие без данных. Проверено на
  Router 0.363.0: Authorization каждого подписчика доходит до lookup, без токена —
  `Unauthenticated`.
- **Транспорт к клиенту — SSE**, а не WebSocket: Router поддерживает оба. Каждая подписка —
  отдельный HTTP-запрос с тем же `Authorization`, CORS и Cloudflare, что и у query, без
  протокола `connection_init`, sticky-сессий и состояния соединения. UI использует `graphql-sse`
  в режиме distinct connections через `subscriptionExchange` urql.
- **Ничего не теряется.** Router отправляет только изменения после подписки. Поэтому клиент
  перезапрашивает активные query при каждом (пере)подключении потока: изменение между query и
  подпиской или во время разрыва попадает в ответ query. Подписка заказа закрывается, когда он
  достиг финального статуса.
- **Polling в UI запрещён целиком.** Ни экран заказа, ни flow board не опрашивают BFF по
  таймеру. Таймеры остаются только для продления токенов к моменту истечения.
- **Без брокера Router не стартует.** `skip_unavailable_providers: false` (default), Helm требует
  `kafka.brokers`.
- **Высокочастотные данные — в событии, права — каналом-капабилити.** Позиции курьера
  (change `add-live-courier-tracking`, FR-661, FR-668) идут раз в секунду: notification + refetch
  означал бы `GetOrderTracking` и check SpiceDB на каждую точку каждого подписчика. Поэтому
  `courierMoved(channel)` читает канал Valkey `delivery.tracks.<channel>` провайдером `redis`
  Cosmo Streams (не Kafka: точка не Integration Event и не идёт через Outbox), а событие несёт
  все поля `CourierPosition`. Event-driven subgraph может объявлять только поля ключа, поэтому
  ключ `CourierPosition` — все её поля (`@key(fields: "channel latitude longitude recordedAt",
  resolvable: false)` в обоих subgraphs): Router берёт их из события и plugin не вызывает.
  Права проверяются один раз — когда `delivery` выдаёт канал в `Order.tracking` по `order#view`:
  канал — 128 случайных бит, без него подписаться не на что. После завершения доставки в канал
  никто не публикует. Шаблон `{{ args.channel }}` в `channels` и доставка без вызова plugin
  проверены на Router 0.363.0. Router подписывается `PSUBSCRIBE`, а ACL Valkey сравнивает шаблон
  PSUBSCRIBE буквально, поэтому у ACL-пользователя `bff` `allchannels` и только команды
  подписки.

## Consequences

- Нагрузка пропорциональна изменениям: на изменение одного заказа приходится один `GetOrder` на
  каждого подписчика этого заказа. Опросы без изменений исчезают.
- Router читает Kafka без consumer group: каждая реплика получает весь топик и фильтрует по
  `id`. Объём — одно маленькое сообщение на изменение заказа. При росте нагрузки нужны топики по
  ключу (шаблон `orders.v1.order-changes.{{ args.id }}` не подходит: Router не создаёт топики)
  или Custom Module.
- Подписчик чужого `id` узнаёт только момент изменения (error-событие), но не данные. `order_id`
  — UUID, перебор нереален. Ранний отказ при старте подписки потребовал бы Custom Module
  `SubscriptionOnStart`, то есть собственную сборку Router.
- В kind BFF получает egress к Kafka и к Valkey `delivery` (`manifests/netpol/app-infrastructure.yaml`);
  без провайдера `tracks` (`BFF__TRACKS_REDIS_URL`) Router не стартует.
- Канал-капабилити — подписка без проверки прав на каждое событие: тот, кто получил канал
  заказа, видит позиции курьера до конца доставки. Для данных, которые могут стать чужими
  посреди потока, этот приём не годится — там остаётся notification + refetch.
- Клиентская схема теперь — композиция subgraphs (`boundaries/bff/client-schema.graphql`,
  генерируется `npm run compose`). Codegen и моки UI читают её, а не схему plugin.
- Моки UI выполняют подписки внутри процесса (`mocks/subscriptions.ts`) на том же mock store:
  MSW в Node не умеет держать и прерывать потоковые ответы fetch.
- Переконфигурация Router разрывает активные подписки. `graphql-sse` переподключается
  бесконечно, после переподключения query перезапрашиваются.
