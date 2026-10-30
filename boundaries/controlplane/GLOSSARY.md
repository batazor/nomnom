# Control plane

Глобальный реестр топологии cell-based архитектуры ([ADR 41](../../docs/adr/0041-cell-based-architecture-with-control-plane.md)):
какие Cell есть и в какой Cell размещён каждый Market. Реестр — ресурсы `Cell` и `Market` Kubernetes.
Control plane не участвует в обработке бизнес-запросов: из реестра он пишет маршруты публичного gateway,
а ни gateway, ни ячейки к нему не обращаются.

## Language

**Cell**:
Полный изолированный экземпляр data plane со своим Cosmo Router, BFF и internal gateway; `cell_id` — slug. Неизменяема после объявления.
_Avoid_: Cluster, region, shard

**Market**:
Город, в котором работает доставка; `market_id` — slug, название 1..64 символа, валюта ISO 4217, часовой пояс IANA (`Europe/Berlin`). Объявляется сразу с Cell; пока этой Cell нет, он не публикуется.
_Avoid_: Tenant, region, city id

**Market Placement**:
Размещение Market ровно в одной объявленной Cell (`spec.cellRef` Market).
_Avoid_: Partition, assignment

**Router Cell**:
Kubernetes Service Cosmo Router Cell (`namespace`, `name`, `port`) — точка входа Cell для клиентов, куда публичный gateway ведёт её Market.
_Avoid_: Gateway address, endpoint

**Маршрут Market**:
`HTTPRoute market-<market_id>` публичного gateway: `/m/<market_id>` в Router Cell его Market Placement. Пишет только control plane.
_Avoid_: Placement Map, routing table

**Список Market**:
JSON `GET /markets`, который Envoy публичного gateway отдаёт сам из `ConfigMap markets`; его строит control plane.
_Avoid_: Markets query

**Архивный Market**:
Market со `spec.archived: true`: нет в Списке Market и Маршрутах Market; ресурс остаётся, `market_id` не создаётся снова, из архива Market не возвращается.
_Avoid_: Deleted market, removed market

**Idempotent Repeat**:
Повторное применение `Cell` или `Market` с теми же значениями ничего не меняет; другие значения для известного id отклоняет API server.
_Avoid_: Upsert, update
