# Glossary Map

Проект разделён на bounded contexts, каждый из которых владеет своим языком и моделью. Одинаковые названия в разных контекстах не означают общий тип или общую реализацию.

## Contexts

- [Orders](boundaries/orders/GLOSSARY.md): оформленный заказ и его общий жизненный цикл.
- [Restaurants](boundaries/restaurants/GLOSSARY.md): меню, принятие заказа рестораном и приготовление.
- [Delivery](boundaries/delivery/GLOSSARY.md): доступность курьеров, назначение и выполнение доставки.
- [Payments](boundaries/payments/GLOSSARY.md): авторизация, отмена авторизации, списание и возврат платежа.
- [Control plane](boundaries/controlplane/GLOSSARY.md): реестр Cell, Market и Market Placement (ADR 41).

## Relationships

Upstream владеет контрактом, downstream под него подстраивается; направление вызова может быть обратным. `orders` сам вызывает соседей, но зависит от их контрактов. Контракты описаны в `boundaries/<context>/infrastructure/grpc`.

| Upstream | Downstream | Паттерн | Где в коде |
| --- | --- | --- | --- |
| `restaurants` | `orders` | Open Host Service и Published Language → Anticorruption Layer: gRPC-команды и запросы, события `restaurants.v1.restaurant-order-events` | `orders/slices/order/infrastructure/{catalog,estimates,scheduledslots,messaging}`, `orders/slices/fulfillment/infrastructure/neighbours/grpcclient`, `orders/slices/simulator/infrastructure/restaurants` |
| `delivery` | `orders` | Open Host Service и Published Language → Anticorruption Layer: gRPC, события `delivery.v1.delivery-events` | `orders/slices/order/infrastructure/{estimates,messaging}`, `orders/slices/fulfillment/infrastructure/neighbours/grpcclient` |
| `payments` | `orders` | Open Host Service и Published Language → Anticorruption Layer: только gRPC, результат операции приходит в ответе | `orders/slices/fulfillment/infrastructure/neighbours/grpcclient` |
| `orders`, `restaurants`, `delivery` | `bff` | Open Host Service и Published Language → Anticorruption Layer: gRPC, а также топики `*-changes` для подписок через Cosmo Streams | `bff/plugins/bff/slices/*/infrastructure`, `bff/events/schema.graphql`. SDK `payments` BFF не использует |
| `controlplane` | `bff` | Published Language: заголовок `X-Nomnom-Market` в маршрутах gateway; прямых вызовов нет | `controlplane/slices/topology/infrastructure/gatewayroutes`, `bff/plugins/bff/infrastructure/market` |
| `restaurants`, `delivery`, `payments` между собой | — | Separate Ways: не импортируют контракты друг друга, процесс связывает workflow `orders` | Импортов SDK соседей в этих контекстах нет |
| `orders`, `restaurants`, `delivery`, `payments` | — | Shared Kernel: схема прав меняется по согласованию, например `order.view` ссылается на `restaurant->view_orders` и `delivery->act_as_courier` | `authorization/schema.zed` |

Других общих частей кода нет: идентификаторы, суммы и сведения о заказе передаются как данные интеграционных контрактов, а доменные агрегаты и value objects совместно не используются. Event Envelope тоже не общий пакет: это единый формат Kafka headers (ADR 25), который каждый контекст заполняет сам.
